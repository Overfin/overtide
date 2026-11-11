// Package sonarrdriver is how Overtide gets episodes and seasons out of
// Sonarr: the wanted episodes and their searches, the airing seasons kept
// coming, whole-season packs and their follow-up, the finished downloads
// cleared from the queue, and, with Sonarr's auto-import off, the imports.
// What to fetch and when is the adding side's (internal/adding): it calls
// the driver with a number of searches, and the driver says how many it
// used.
package sonarrdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// episodesKey keeps, per Sonarr, the series whose new episodes are wanted
// and the missing episodes to search for.
const episodesKey = "adding.episodes"

// Driver does what Sonarr is asked for. What it needs of the adding side
// comes through its fields, set by the adding side.
type Driver struct {
	inv *inventory.Service
	now func() time.Time
	// Room says why nothing may be added to a folder now, or "".
	Room func(cfg config.Config, folder string) string
	// Managed reports whether a series is Overtide's.
	Managed func(m model.Media) bool
	// Ready, Unavailable and Stopped are the trackers' gate, the run's:
	// Ready says whether one more search may go, Unavailable whether the
	// searches since then missed some trackers, Stopped whether either
	// stopped the run's searches. Ready and Unavailable stop them.
	Ready       func() bool
	Unavailable func(since time.Time) (bool, string)
	Stopped     func() bool
	// DailyLimit says whether n more downloads may start now, under the
	// titles a day; Started counts n downloads started now.
	DailyLimit func(n int) bool
	Started    func(n int) error
	// Upgraded counts one upgrade search now, under the upgrades a day.
	Upgraded func() error
	// SeasonValue is a season's score, 0 when unknown: the most valued go
	// first.
	SeasonValue func(ctx context.Context, cfg config.Config, serviceID string, seriesID, season int) float64
	// Step says what's done with a title now, for the Adding page.
	Step func(title, what string)
	// preview is the wanted episodes a dry run found, which it doesn't
	// save: what ReplaceSeasons counts in dry run.
	preview map[string]*State
	// autoImportOff: the Sonarrs whose auto-import is off, as of this run:
	// Overtide imports for them. ordersCache: what Overfin said of series.
	autoImportOff map[string]bool
	ordersCache   map[int]cachedOrders
	// choicesMu guards the operator's choices, which come while a run goes.
	choicesMu sync.Mutex
}

// New is a driver that stops nothing and counts nothing until its fields
// say otherwise.
func New(inv *inventory.Service, now func() time.Time) *Driver {
	return &Driver{
		inv:         inv,
		now:         now,
		Room:        func(config.Config, string) string { return "" },
		Managed:     func(model.Media) bool { return false },
		Ready:       func() bool { return true },
		Unavailable: func(time.Time) (bool, string) { return false, "" },
		Stopped:     func() bool { return false },
		DailyLimit:  func(int) bool { return true },
		Started:     func(int) error { return nil },
		Upgraded:    func() error { return nil },
		SeasonValue: func(context.Context, config.Config, string, int, int) float64 { return 0 },
		Step:        func(string, string) {},
	}
}

// EpisodeWant is a missing episode Overtide searches for.
type EpisodeWant struct {
	SeriesID int       `json:"seriesId"`
	Season   int       `json:"season"`
	Tries    int       `json:"tries,omitempty"`
	NextTry  time.Time `json:"nextTry,omitempty"`
	// Grabbed: its search started a download, followed until imported.
	Grabbed bool `json:"grabbed,omitempty"`
	// AiredAt is when it aired: searched every day until tries.ReleaseGrace
	// after it, and before anything else meanwhile. Episode is its number;
	// both are filled in once known.
	AiredAt time.Time `json:"airedAt,omitzero"`
	Episode int       `json:"episode,omitempty"`
	// LastTry and Result are the last search and what came of it.
	LastTry time.Time `json:"lastTry,omitzero"`
	Result  string    `json:"result,omitempty"`
	// ByHand: its series was added from outside (by hand, by an import
	// list, by another app) while Overtide manages everything: searched
	// before anything else (SearchByHand).
	ByHand bool `json:"byHand,omitempty"`
}

// State is what Overtide keeps for one Sonarr.
type State struct {
	// Follow: series whose new episodes are wanted, as Sonarr would have
	// fetched them (followed series, or with their last season monitored).
	Follow map[int]bool `json:"follow"`
	// Wanted: missing episodes that were monitored when Overtide took
	// their series, that aired without coming through the RSS, or of a
	// season otherwise incomplete.
	Wanted map[int]EpisodeWant `json:"wanted"`
	// Seasons: seasons replaced, or to be, by a whole-season pack, by
	// seasonKey.
	Seasons map[string]*SeasonPack `json:"seasons,omitempty"`
	// Looked is when incomplete seasons were last looked for.
	Looked time.Time `json:"lookedAt,omitzero"`
	// Imports: the downloads Overtide imports, by download ID, with Sonarr's
	// auto-import off since ImportsOffSince. ImportsRead is how far its grabs
	// were read.
	Imports         map[string]*downloadImport `json:"imports,omitempty"`
	ImportsOffSince time.Time                  `json:"importsOffSince,omitzero"`
	ImportsRead     time.Time                  `json:"importsRead,omitzero"`
}

// States returns what's kept for each Sonarr, by its ID.
func (driver *Driver) States() (map[string]*State, error) {
	states := map[string]*State{}
	raw, err := driver.inv.Store().Meta(episodesKey)
	if err != nil || raw == "" {
		return states, err
	}
	if err := json.Unmarshal([]byte(raw), &states); err != nil {
		return nil, fmt.Errorf("read the wanted episodes: %w", err)
	}
	// What the operator removed by hand is never there for a run.
	removed := driver.Removed()
	for serviceID, state := range states {
		if state != nil {
			removed.dropRemoved(serviceID, state)
		}
	}
	return states, nil
}

// SaveStates keeps what's kept for each Sonarr.
func (driver *Driver) SaveStates(states map[string]*State) error {
	encoded, err := json.Marshal(states)
	if err != nil {
		return err
	}
	return driver.inv.Store().SetMeta(episodesKey, string(encoded))
}

func stateFor(states map[string]*State, serviceID string) *State {
	state := states[serviceID]
	if state == nil {
		state = &State{}
		states[serviceID] = state
	}
	if state.Follow == nil {
		state.Follow = map[int]bool{}
	}
	if state.Wanted == nil {
		state.Wanted = map[int]EpisodeWant{}
	}
	if state.Seasons == nil {
		state.Seasons = map[string]*SeasonPack{}
	}
	return state
}

// Importing returns whether a series has a download Overtide imports that
// isn't settled yet (the Sonarr's auto-import off): a download that left
// the queue without its files isn't to be taken for one that stopped. It
// reads what's kept once.
func (driver *Driver) Importing() func(serviceID string, seriesID int) bool {
	states, _ := driver.States()
	return func(serviceID string, seriesID int) bool {
		return driver.autoImportOff[serviceID] && states[serviceID].importing(seriesID)
	}
}

// Upgradable returns whether a season may be searched for an upgrade: it's
// out, and none of its episodes is wanted. It reads what's kept once.
func (driver *Driver) Upgradable() (func(ctx context.Context, serviceID string, seriesID, season int) (bool, error), error) {
	states, err := driver.States()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, serviceID string, seriesID, season int) (bool, error) {
		if state := states[serviceID]; state != nil {
			for _, want := range state.Wanted {
				if want.SeriesID == seriesID && want.Season == season {
					return false, nil
				}
			}
		}
		client, err := driver.inv.SonarrClient(serviceID)
		if err != nil {
			return false, err
		}
		episodes, err := client.WithContext(ctx).Episodes(seriesID)
		if err != nil {
			return false, nil // asked again next run
		}
		return seasonOut(episodes, season, driver.now()), nil
	}, nil
}

// fullDisk says whether a Sonarr series' folder is on a disk with no room
// (Room, asked once per series): nothing more is fetched for it then. A
// series whose folder isn't read yet (just added) is asked of Sonarr; if it
// can't say, it counts as full, or a failed refresh would switch the disk
// checks off.
func (driver *Driver) fullDisk(cfg config.Config, serviceID string) func(seriesID int) bool {
	folders := map[int]string{}
	if items, _, err := driver.inv.Snapshot(); err == nil {
		for _, m := range items {
			if m.ServiceID == serviceID && m.Type == model.Series {
				folders[m.SourceID] = m.Path
			}
		}
	}
	full := map[int]bool{}
	return func(seriesID int) bool {
		if checked, ok := full[seriesID]; ok {
			return checked
		}
		folder := folders[seriesID]
		var unknown error
		if folder == "" {
			var client *sonarr.Client
			if client, unknown = driver.inv.SonarrClient(serviceID); unknown == nil {
				folder, unknown = client.SeriesPath(seriesID)
			}
		}
		full[seriesID] = unknown != nil || (folder != "" && driver.Room(cfg, folder) != "")
		return full[seriesID]
	}
}

// seriesNames are the series of a Sonarr by ID, as last read.
func (driver *Driver) seriesNames(serviceID string) map[int]string {
	names := map[int]string{}
	if items, _, err := driver.inv.Snapshot(); err == nil {
		for _, m := range items {
			if m.ServiceID == serviceID && m.Type == model.Series {
				names[m.SourceID] = m.Title
			}
		}
	}
	return names
}

func seasonName(series string, season int) string {
	return fmt.Sprintf("%s S%d", series, season)
}

func episodeName(series string, season, episode int) string {
	return fmt.Sprintf("%s S%dE%d", series, season, episode)
}
