package sonarrdriver

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/tries"
)

// What the operator took out of Overtide's lists by hand (the Adding
// pages' Remove): kept on its own, written at once and never by a run, so
// a run in progress can't bring it back, and read by every run.
//
// Titles stay out while Radarr's or Sonarr's import-list exclusion (made
// with the removal) keeps them out, which the operator can undo there:
// RemovedTitleFor is only how long the list keeps them out on its own
// meanwhile. Seasons and episodes have no such list in Sonarr: they stay
// out.

const removedKey = "adding.removed"

// RemovedTitleFor is how long a title removed by hand is kept out by
// Overtide itself, before only Radarr's or Sonarr's exclusion does.
const RemovedTitleFor = 30 * 24 * time.Hour

// Removed is what was removed by hand, each with when.
type Removed struct {
	// Titles, by the waiting list's entry ID (service|key).
	Titles map[string]time.Time `json:"titles,omitempty"`
	// Seasons, by SeasonRef; Episodes, by EpisodeRef.
	Seasons  map[string]time.Time `json:"seasons,omitempty"`
	Episodes map[string]time.Time `json:"episodes,omitempty"`
	// Upgrades never searched for, by UpgradeRow.Ref.
	Upgrades map[string]time.Time `json:"upgrades,omitempty"`
}

// SeasonRef names a season of a Sonarr's series in Removed.
func SeasonRef(serviceID string, seriesID, season int) string {
	return fmt.Sprintf("%s|%d/%d", serviceID, seriesID, season)
}

// EpisodeRef names a Sonarr's episode in Removed.
func EpisodeRef(serviceID string, episodeID int) string {
	return fmt.Sprintf("%s|%d", serviceID, episodeID)
}

var removedMu sync.Mutex

// Removed reads what was removed by hand; nothing when it can't.
func (driver *Driver) Removed() Removed {
	var out Removed
	if driver.inv == nil || driver.inv.Store() == nil {
		return out
	}
	raw, err := driver.inv.Store().Meta(removedKey)
	if err == nil && raw != "" {
		json.Unmarshal([]byte(raw), &out)
	}
	now := driver.now()
	for id, at := range out.Titles {
		if now.Sub(at) >= RemovedTitleFor {
			delete(out.Titles, id)
		}
	}
	return out
}

// Remove records what the operator removed by hand.
func (driver *Driver) Remove(change func(r *Removed, now time.Time)) error {
	removedMu.Lock()
	defer removedMu.Unlock()
	r := driver.Removed()
	if r.Titles == nil {
		r.Titles = map[string]time.Time{}
	}
	if r.Seasons == nil {
		r.Seasons = map[string]time.Time{}
	}
	if r.Episodes == nil {
		r.Episodes = map[string]time.Time{}
	}
	if r.Upgrades == nil {
		r.Upgrades = map[string]time.Time{}
	}
	change(&r, driver.now())
	encoded, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return driver.inv.Store().SetMeta(removedKey, string(encoded))
}

// dropRemoved takes out of a Sonarr's state the seasons and episodes
// removed by hand, but those downloading (their download is followed to
// its end).
func (r Removed) dropRemoved(serviceID string, state *State) {
	if len(r.Seasons) == 0 && len(r.Episodes) == 0 {
		return
	}
	for key, pack := range state.Seasons {
		if _, out := r.Seasons[SeasonRef(serviceID, pack.SeriesID, pack.Season)]; out && pack.GrabbedAt.IsZero() {
			delete(state.Seasons, key)
		}
	}
	for id, want := range state.Wanted {
		_, episode := r.Episodes[EpisodeRef(serviceID, id)]
		_, season := r.Seasons[SeasonRef(serviceID, want.SeriesID, want.Season)]
		if (episode || season) && !want.Grabbed {
			delete(state.Wanted, id)
		}
	}
}

// ErrNotWanted: no such season or episode in the lists, or downloading.
var ErrNotWanted = errors.New("not in the list")

// RemoveWanted takes a season or an episode (kind) out of the lists by
// hand, by the Ref its row gave: one in the lists, and not downloading.
func (driver *Driver) RemoveWanted(kind, ref string) error {
	episodes, seasons, err := driver.Wanted()
	if err != nil {
		return err
	}
	rows := episodes
	if kind == "season" {
		rows = seasons
	}
	for _, row := range rows {
		if row.Ref == ref && row.Removable {
			return driver.Remove(func(r *Removed, now time.Time) {
				if kind == "season" {
					r.Seasons[ref] = now
				} else {
					r.Episodes[ref] = now
				}
			})
		}
	}
	return ErrNotWanted
}

// PackRef names a season's whole-season pack upgrade in Removed.Upgrades.
func PackRef(serviceID string, seriesID, season int) string {
	return "pack|" + SeasonRef(serviceID, seriesID, season)
}

// PackUpgradeRow is a season that came complete without its whole-season
// pack, waiting for one (PackUpgrades).
type PackUpgradeRow struct {
	ServiceID        string
	SeriesID, Season int
	Title            string
	Tries            int
	NextTry          time.Time
}

// PackUpgradeRows lists the seasons PackUpgrades would search a pack for,
// but those removed by hand.
func (driver *Driver) PackUpgradeRows() ([]PackUpgradeRow, error) {
	states, err := driver.States()
	if err != nil {
		return nil, err
	}
	removed := driver.Removed()
	var out []PackUpgradeRow
	for serviceID, state := range states {
		if state == nil {
			continue
		}
		names := driver.seriesNames(serviceID)
		wanting := map[string]bool{}
		for _, want := range state.Wanted {
			wanting[seasonKey(want.SeriesID, want.Season)] = true
		}
		for key, pack := range state.Seasons {
			if pack.Done || pack.short() || !pack.GrabbedAt.IsZero() || pack.Tries == 0 || pack.Tries >= tries.Max || wanting[key] {
				continue
			}
			if _, out := removed.Upgrades[PackRef(serviceID, pack.SeriesID, pack.Season)]; out {
				continue
			}
			if _, there := names[pack.SeriesID]; !there {
				continue
			}
			out = append(out, PackUpgradeRow{ServiceID: serviceID, SeriesID: pack.SeriesID, Season: pack.Season,
				Title: seasonName(names[pack.SeriesID], pack.Season), Tries: pack.Tries, NextTry: pack.NextTry})
		}
	}
	return out, nil
}
