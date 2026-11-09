// Package adding brings titles into the libraries: it keeps a waiting list
// of titles to add, what people wished for first (in the turns Overfin
// gives), then what Overfin suggests, and tries them one by one. Overfin gives opinions;
// Overtide decides. A title goes in Radarr or Sonarr once, unmonitored
// and tagged, and is never deleted from them: a try monitors it and
// searches for it, and if nothing started it's unmonitored again until
// its next try. Import lists are the operator's business: Overtide never
// reads them. Episodes and seasons go through the Sonarr driver
// (internal/sonarrdriver): this side says when, and within how many
// searches, and counts them.
package adding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/sonarrdriver"
	"github.com/overfin/overtide/internal/tries"
)

// tagFor is the tag a title gets when Overtide adds it, by where it came
// from; "" for a title already there (it has its tag from being taken).
func tagFor(source Source) string {
	switch source {
	case FromRequest:
		return model.TagRequested
	case FromOverfin:
		return model.TagAdded
	}
	return ""
}

// RunEvery is how often the titles are tried, and RunFor how long one run
// goes on trying titles, one search at a time: under RunEvery, so runs
// never overlap. What's left is tried by the next run. ReadEvery is how
// often Overfin's suggestions are read (ReadOverfin).
const (
	RunEvery  = 15 * time.Minute
	RunFor    = 14 * time.Minute
	ReadEvery = time.Hour
)

// The waiting list's lifecycle, fixed for now (see the README); the tries'
// and the history's cooldown are in internal/tries.
const (
	// WaitingMax is how many suggestions may wait; when full, no new one
	// is taken.
	WaitingMax = 200
	// MaxAge: a suggestion leaves after that long in the list, as after
	// tries.Max tries that found nothing.
	MaxAge = 60 * 24 * time.Hour
)

const (
	storeKey   = "adding.waiting"
	historyKey = "adding.history"
	// tagsKey records that the former tags (Overswell's, and Overtide's
	// own "overtide" from before the split) were replaced, once.
	tagsKey = "adding.tags_overtide"
)

// Source is where a title in the waiting list comes from: what the
// operator added by hand in Radarr since "Manage all" goes first (they want
// it now), then requests (older incomplete seasons come right after them,
// see Run), then the other titles "Manage all" found waiting in Radarr,
// then Overfin's suggestions, in Overfin's order.
type Source string

const (
	FromHand    Source = "hand"
	FromRequest Source = "request"
	FromLibrary Source = "library"
	FromOverfin Source = "overfin"
)

func (source Source) rank() int {
	switch source {
	case FromHand:
		return 0
	case FromRequest:
		return 1
	case FromLibrary:
		return 2
	}
	return 3
}

// State is where a title stands.
type State string

const (
	// Waiting: to be tried at NextTry.
	Waiting State = "waiting"
	// NotReleased: not out yet. Unmonitored (ArrID 0 or not monitored) it
	// waits for tries.ReleaseWindow before its release (NextTry); monitored
	// (MonitoredAt set), follow looks for the grab until tries.ReleaseGrace
	// after the release.
	NotReleased State = "not released"
	// Downloading: a try started a download, and the title is unmonitored;
	// followed until imported, or back to Waiting if the download stops.
	Downloading State = "downloading"
	// Imported: the title has its files.
	Imported State = "imported"
)

// Entry is one title of the waiting list.
type Entry struct {
	// Key is movie:<TMDB ID> or series:<TVDB ID>.
	Key        string          `json:"key"`
	Kind       model.MediaType `json:"kind"`
	ServiceID  string          `json:"serviceId"`
	ExternalID int             `json:"externalId"`
	Title      string          `json:"title"`
	Year       int             `json:"year"`
	Source     Source          `json:"source"`
	// Seasons are a series' seasons to add, as Overfin suggests them.
	Seasons []int `json:"seasons,omitempty"`
	// Categories are Overfin's categories it fits: where it goes depends
	// on them (config.DestinationFor).
	Categories []string `json:"categories,omitempty"`
	// Position is its place in its source's order.
	Position int   `json:"position"`
	State    State `json:"state"`
	// ArrID is its ID in Radarr or Sonarr once it's there.
	ArrID int `json:"arrId,omitempty"`
	// Unmonitored: unmonitored once its download was confirmed, so Radarr
	// and Sonarr don't upgrade it on their own.
	Unmonitored bool      `json:"unmonitored,omitempty"`
	Tries       int       `json:"tries"`
	NextTry     time.Time `json:"nextTry,omitempty"`
	// ReleaseAt and MonitoredAt: a title monitored for its release.
	ReleaseAt   time.Time `json:"releaseAt,omitempty"`
	MonitoredAt time.Time `json:"monitoredAt,omitempty"`
	Result      string    `json:"result,omitempty"`
	// WishTMDB is a wish's TMDB ID (a film's, or its series'), to tell
	// Overfin what became of it; Reported, what was last told, so a state
	// is told once.
	WishTMDB int    `json:"wishTmdb,omitempty"`
	Reported string `json:"reported,omitempty"`
	// AddedAt is when it joined the list (MaxAge).
	AddedAt   time.Time `json:"addedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (entry Entry) id() string { return entry.ServiceID + "|" + entry.Key }

// searches is how many searches a try of the entry asks for: one for a
// film, one per season for a series.
func (entry Entry) searches() int {
	if entry.Kind == model.Series && len(entry.Seasons) > 0 {
		return len(entry.Seasons)
	}
	return 1
}

// waiting reports whether the entry still waits to be added: what the
// cap and the lifecycle count.
func (entry Entry) waiting() bool {
	return entry.State == Waiting || (entry.State == NotReleased && entry.MonitoredAt.IsZero())
}

func movieKey(tmdbID int) string  { return fmt.Sprintf("movie:%d", tmdbID) }
func seriesKey(tvdbID int) string { return fmt.Sprintf("series:%d", tvdbID) }

// Service keeps the waiting list and tries its titles; episodes and seasons
// go through the Sonarr driver.
type Service struct {
	inv *inventory.Service
	// mu makes runs and reads take turns.
	mu  sync.Mutex
	now func() time.Time
	// disk replaces diskStop in tests.
	disk func(cfg config.Config, rootFolder string, dryRun bool) string
	// values are the library's scores, read once a run when asked (see
	// libraryValues).
	values *overfin.Values
	sonarr *sonarrdriver.Driver
	// Room, when set, says why nothing may be added to a root folder now,
	// or "": past its disk's critical threshold, say. In dry run, which
	// adds nothing, a folder whose disk isn't known yet is let through.
	Room func(rootFolder string, dryRun bool) string
	// Trackers, when set, says whether the searches made since then missed
	// some trackers (trackers.Gate.Unavailable): a search that found
	// nothing then doesn't count as a try.
	Trackers func(since time.Time) (bool, string)
	// Ready, when set, says whether the trackers have room for one more
	// search, and if not why: asked before each search.
	Ready  func() (bool, string)
	liveMu sync.Mutex
	live   Live
	// added is when this service last added a title to Radarr or Sonarr
	// (see OwnAddRecent).
	addedMu sync.Mutex
	added   time.Time
	// trackersDown is why this run's searches stopped: the trackers were
	// unavailable.
	trackersDown string
	// Radarr's and Sonarr's events (events.go): where the panel was opened,
	// when the last test event came, what keeps them from coming, and the
	// webhooks saved.
	connectMu     sync.Mutex // one connect at a time
	eventsMu      sync.Mutex
	seenAt        string
	heard         time.Time
	eventsProblem string
	connected     map[string]bool
}

// unavailable reports whether the searches since start missed trackers;
// it notes why, which stops the run's searches.
func (service *Service) unavailable(start time.Time) (bool, string) {
	if service.Trackers == nil {
		return false, ""
	}
	down, why := service.Trackers(start)
	if down {
		service.trackersDown = why
	}
	return down, why
}

// ready reports whether one more search may go: no tracker was missed
// this run, and Ready says they have room. If not, it notes why, which
// stops the run's searches.
func (service *Service) ready() bool {
	if service.trackersDown != "" {
		return false
	}
	if service.Ready == nil {
		return true
	}
	if ok, why := service.Ready(); !ok {
		service.trackersDown = why
		return false
	}
	return true
}

func New(inv *inventory.Service) *Service {
	service := &Service{inv: inv, now: time.Now, connected: map[string]bool{}}
	driver := sonarrdriver.New(inv, func() time.Time { return service.now() })
	driver.Room = func(cfg config.Config, folder string) string {
		disk := service.diskStop
		if service.disk != nil {
			disk = service.disk
		}
		return disk(cfg, folder, false)
	}
	driver.Managed = service.managed
	driver.Ready = service.ready
	driver.Unavailable = service.unavailable
	driver.Stopped = func() bool { return service.trackersDown != "" }
	driver.DailyLimit = func(n int) bool {
		ok, until := service.DailyLimit(n)
		if !ok {
			service.setLive(func(l *Live) { l.DailyUntil = until })
		}
		return ok
	}
	driver.Started = service.recordStarted
	driver.Upgraded = func() error { return service.recordNow(upgradesKey, 1) }
	driver.SeasonValue = service.seasonValue
	driver.Step = service.step
	service.sonarr = driver
	return service
}

// Sonarr is the driver episodes and seasons go through: what the Adding
// page shows of them, and the operator's import choices.
func (service *Service) Sonarr() *sonarrdriver.Driver { return service.sonarr }

// Clear empties the waiting list, but for requests and the titles "Manage
// all" put in line (nothing else would bring them back): Overfin's
// suggestions come back at its next reading, in its order. What's moving
// (downloading, imported, monitored for its release) stays. It returns how
// many titles left.
func (service *Service) Clear() (int, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	entries, err := service.load()
	if err != nil {
		return 0, err
	}
	kept := entries[:0]
	for _, entry := range entries {
		if entry.waiting() && entry.Source != FromRequest && entry.Source != FromLibrary && entry.Source != FromHand {
			continue
		}
		kept = append(kept, entry)
	}
	cleared := len(entries) - len(kept)
	return cleared, service.save(kept)
}

// Entries returns the waiting list in the order titles are tried, as last
// saved: a run saves it whole after each try, so it's read without
// waiting for the run to end.
func (service *Service) Entries() ([]Entry, error) {
	entries, err := service.load()
	if err != nil {
		return nil, err
	}
	sortForTries(entries)
	return entries, nil
}

// titleKey is a Radarr film's or Sonarr series' key in the waiting list,
// or "" without the ID it needs.
func titleKey(m model.Media) string {
	switch {
	case m.Type == model.Movie && m.TMDBID > 0:
		return movieKey(m.TMDBID)
	case m.Type == model.Series && m.TVDBID > 0:
		return seriesKey(m.TVDBID)
	}
	return ""
}

// Run is the trying task: the first time it's on, it replaces the legacy
// tag; with "Manage all", it takes every title (takeAll); then it follows
// what's moving and applies the lifecycle. Searches then go in this order,
// until the run's run out: what the operator added by hand, episodes just aired and seasons just finished
// (at most half of them, the rest at the end if any are left), wishes, older
// incomplete seasons and episodes, the other titles, upgrades.
func (service *Service) Run(ctx context.Context) error {
	cfg := service.inv.Config()
	mode := cfg.Adding.Mode
	if mode != config.AddingDryRun && mode != config.AddingOn {
		return nil
	}
	dryRun := mode == config.AddingDryRun
	service.mu.Lock()
	defer service.mu.Unlock()
	budget := cfg.Adding.SearchesPerRun()
	service.setLive(func(l *Live) { *l = Live{Running: true, DryRun: dryRun, Budget: budget} })
	defer service.setLive(func(l *Live) {
		l.Running, l.Phase, l.Title, l.Step, l.Stopped = false, "", "", "", service.trackersDown
	})
	service.phase("Getting ready")
	entries, err := service.load()
	if err != nil {
		return err
	}
	if !dryRun {
		if err := service.retag(ctx, cfg, entries); err != nil {
			return err
		}
	}
	service.values = nil
	if cfg.Adding.ManagesAll() {
		service.phase("Taking what Radarr and Sonarr have")
		if entries, err = service.takeAll(ctx, cfg, entries, dryRun); err != nil {
			// What was taken before it failed is unmonitored by now, and
			// never taken again: it's kept.
			if !dryRun {
				if saveErr := service.save(entries); saveErr != nil {
					return errors.Join(err, saveErr)
				}
			}
			return err
		}
	} else {
		service.phase("Looking at what Overtide added")
		service.sonarr.Tend(ctx, cfg, activeEntries(entries), dryRun)
	}
	// What people wished for, in Overfin's turns, before anything else.
	entries = service.readWishes(ctx, cfg, entries)
	service.phase("Following the downloads")
	service.sonarr.ImportDownloads(ctx, cfg, dryRun)
	entries = service.match(entries)
	service.follow(ctx, entries, dryRun)
	entries = service.reportWishes(ctx, cfg, entries, dryRun)
	service.sonarr.ClearFinished(ctx, cfg, dryRun)
	if entries, err = service.expire(entries); err != nil {
		return err
	}
	sortForTries(entries)
	stopAt := service.now().Add(RunFor)
	searches := 0
	service.trackersDown = ""
	counted := func(used int) {
		searches += used
		service.setLive(func(l *Live) { l.Searches = searches })
	}
	stopped := func() bool {
		return ctx.Err() != nil || service.now().After(stopAt) || searches >= budget || service.trackersDown != ""
	}
	tryTitles := func(wanted func(Source) bool) error {
		service.phase("Trying titles")
		for i := range entries {
			if stopped() {
				break
			}
			entry := &entries[i]
			if !wanted(entry.Source) || !entry.waiting() || entry.NextTry.After(service.now()) {
				continue
			}
			// A series whose seasons would go past the limit waits for the next
			// run, unless it's the run's first title (it would never fit).
			if searches > 0 && searches+entry.searches() > budget {
				continue
			}
			// Everything that comes in shares the titles a day.
			if ok, until := service.DailyLimit(entry.searches()); !ok {
				service.setLive(func(l *Live) { l.DailyUntil = until })
				continue
			}
			if !service.ready() {
				break
			}
			counted(service.try(ctx, cfg, entry, dryRun))
			entry.UpdatedAt = service.now()
			// A wish's search spends its turn in Overfin at once, so the
			// next read gives the next account's.
			service.reportWish(ctx, cfg, entry, dryRun)
			// Saved after each try: a try changes Radarr or Sonarr, and what
			// it did must not be lost if the next one stops the run.
			if err := service.save(entries); err != nil {
				return err
			}
		}
		return nil
	}
	// What people follow first: episodes just aired, then seasons just
	// finished. Neither waits for the titles a day, but together they take
	// at most half of the run's searches left, so that what comes next gets
	// some. What the others leave comes back to them at the end.
	// What the operator added by hand comes before all that.
	var used int
	err = tryTitles(func(source Source) bool { return source == FromHand })
	if err == nil && !stopped() {
		service.phase("Searching what was added by hand")
		used, err = service.sonarr.SearchByHand(ctx, cfg, budget-searches, dryRun)
		counted(used)
	}
	first := searches + (budget-searches+1)/2
	if err == nil && !stopped() {
		service.phase("Searching airing episodes")
		used, err = service.sonarr.SearchEpisodes(ctx, cfg, first-searches, dryRun, true)
		counted(used)
	}
	if err == nil && !stopped() {
		service.phase("Whole seasons, just finished")
		used, err = service.replaceSeasons(ctx, cfg, first-searches, dryRun, true)
		counted(used)
	}
	// What people wished for, then older incomplete seasons, then the rest.
	if err == nil {
		err = tryTitles(func(source Source) bool { return source == FromRequest })
	}
	if err == nil && !stopped() {
		service.phase("Whole seasons, incomplete")
		used, err = service.replaceSeasons(ctx, cfg, budget-searches, dryRun, false)
		counted(used)
	}
	if err == nil && !stopped() {
		service.phase("Searching missing episodes")
		used, err = service.sonarr.SearchEpisodes(ctx, cfg, budget-searches, dryRun, false)
		counted(used)
	}
	if err == nil {
		err = tryTitles(func(source Source) bool { return source != FromRequest })
	}
	if err == nil && !stopped() {
		service.phase("Searching airing episodes")
		used, err = service.sonarr.SearchEpisodes(ctx, cfg, budget-searches, dryRun, true)
		counted(used)
	}
	if err == nil && !stopped() {
		service.phase("Whole seasons, just finished")
		used, err = service.replaceSeasons(ctx, cfg, budget-searches, dryRun, true)
		counted(used)
	}
	if saveErr := service.save(service.reportWishes(ctx, cfg, entries, dryRun)); saveErr != nil {
		return saveErr
	}
	if err != nil {
		return err
	}
	if stopped() {
		return nil
	}
	service.phase("Searching for upgrades")
	used, err = service.searchUpgrades(ctx, cfg, entries, budget-searches, dryRun)
	counted(used)
	return err
}

// managed reports whether a title in Radarr or Sonarr is Overtide's: one
// of its tags, or every title with "Manage all".
func (service *Service) managed(m model.Media) bool {
	if service.inv.Config().Adding.ManagesAll() {
		return true
	}
	for _, tag := range m.Tags {
		if model.IsManagedTag(tag) {
			return true
		}
	}
	return false
}

// expire takes out the suggestions at the end of their lifecycle: tries.Max
// tries that found nothing, or MaxAge in the list. Each goes to the
// history, so it isn't taken again before the cooldown ends.
func (service *Service) expire(entries []Entry) ([]Entry, error) {
	history, err := service.history()
	if err != nil {
		return entries, err
	}
	now := service.now()
	kept := entries[:0]
	changed := false
	for _, entry := range entries {
		if entry.AddedAt.IsZero() {
			entry.AddedAt = now // saved by an older version
		}
		if entry.Source == FromOverfin && entry.waiting() {
			reason := ""
			switch {
			case entry.Tries >= tries.Max:
				reason = fmt.Sprintf("Nothing found in %d tries", entry.Tries)
			case now.Sub(entry.AddedAt) >= MaxAge:
				reason = "Not added in 60 days"
			}
			if reason != "" {
				history[entry.id()] = historyEvent{At: now, Why: reason, Title: entry.Title}
				changed = true
				continue
			}
		}
		kept = append(kept, entry)
	}
	if changed {
		return kept, service.saveHistory(history)
	}
	return kept, nil
}

// sortForTries orders the waiting list: requests first, then Overfin's
// suggestions, each in its source's order.
func sortForTries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Source.rank() != entries[j].Source.rank() {
			return entries[i].Source.rank() < entries[j].Source.rank()
		}
		return entries[i].Position < entries[j].Position
	})
}

// justTaken is how long a title taken from Radarr stays on the list without
// the inventory having it: longer than a refresh takes to come (30 minutes
// by default).
const justTaken = 2 * time.Hour

// match checks the waiting list against Radarr and Sonarr. A title there
// that isn't Overtide's is the operator's: it leaves the list, as does a
// film that has its file already. One that's Overtide's is tried in place
// (its ID kept); a series in, with files, may still lack the seasons
// suggested. Titles moving (downloading, imported, monitored for their
// release) are left to follow: the inventory may not have caught up with a
// title just added.
func (service *Service) match(entries []Entry) []Entry {
	items, _, _ := service.inv.Snapshot()
	present := map[string]model.Media{}
	for _, m := range items {
		if key := titleKey(m); key != "" {
			present[m.ServiceID+"|"+key] = m
		}
	}
	kept := entries[:0]
	for _, entry := range entries {
		if !entry.waiting() {
			kept = append(kept, entry)
			continue
		}
		m, there := present[entry.id()]
		switch {
		case !there && (entry.Source == FromLibrary || entry.Source == FromHand):
			// Taken from Radarr, and gone from it since; unless just taken:
			// the inventory hasn't caught up with a title just added, and
			// it was unmonitored, so it's never taken again.
			if entry.ArrID == 0 || service.now().Sub(entry.AddedAt) > justTaken {
				continue
			}
		case there && model.HasTag(m.Tags, model.TagIgnore):
			continue // left alone by the operator
		case !there:
			entry.ArrID = 0
		case !service.managed(m):
			continue
		case entry.Kind == model.Movie && m.SizeBytes > 0:
			continue
		default:
			entry.ArrID = m.SourceID
		}
		kept = append(kept, entry)
	}
	return kept
}

// follow asks Radarr and Sonarr about the titles moving: downloading ones
// are imported once they have files; one monitored for its release is
// downloading once grabbed, or, nothing grabbed tries.ReleaseGrace after
// the release, unmonitored and counted as a try that found nothing. A title
// that left Radarr or Sonarr waits for another try.
func (service *Service) follow(ctx context.Context, entries []Entry, dryRun bool) {
	importing := service.sonarr.Importing()
	ignored := map[string]bool{}
	if items, _, err := service.inv.Snapshot(); err == nil {
		for _, m := range items {
			if model.HasTag(m.Tags, model.TagIgnore) {
				ignored[fmt.Sprintf("%s|%s|%d", m.ServiceID, m.Type, m.SourceID)] = true
			}
		}
	}
	for i := range entries {
		entry := &entries[i]
		if ignored[fmt.Sprintf("%s|%s|%d", entry.ServiceID, entry.Kind, entry.ArrID)] {
			continue // left alone by the operator
		}
		monitoredForRelease := entry.State == NotReleased && !entry.MonitoredAt.IsZero()
		moving := entry.State == Downloading || entry.State == Imported
		if entry.ArrID == 0 || (!moving && !monitoredForRelease) {
			continue
		}
		a, err := service.arrFor(ctx, entry)
		if err != nil {
			continue
		}
		if moving && !entry.Unmonitored && !dryRun {
			// Saved by a version that left it monitored, or the unmonitoring
			// failed: Radarr and Sonarr mustn't upgrade it on their own.
			if a.Monitor(entry.ArrID, false) == nil {
				entry.Unmonitored = true
				entry.UpdatedAt = service.now()
			}
		}
		if entry.State == Imported {
			continue
		}
		if entry.State == Downloading {
			hasFiles, err := a.HasFiles(entry.ArrID)
			switch {
			case a.Gone(err):
				entry.State, entry.ArrID, entry.NextTry = Waiting, 0, service.now()
				entry.Result = "No longer in " + a.Name
			case err != nil:
				continue
			case hasFiles:
				entry.State, entry.Result = Imported, "Imported"
			default:
				// Unmonitored, it isn't downloaded again on its own: a
				// download that left the queue without its files goes back
				// in line.
				queued, err := a.Queued(entry.ArrID)
				if err != nil || queued {
					continue
				}
				// Overtide's own import, not settled yet.
				if entry.Kind == model.Series && importing(entry.ServiceID, entry.ArrID) {
					continue
				}
				if hasFiles, err = a.HasFiles(entry.ArrID); err != nil {
					continue
				} else if hasFiles {
					entry.State, entry.Result = Imported, "Imported"
				} else {
					service.failed(entry, "The download stopped before it was imported")
				}
			}
			entry.UpdatedAt = service.now()
			continue
		}
		grabbed, err := a.Grabbed(entry.ArrID, entry.MonitoredAt)
		switch {
		case a.Gone(err):
			entry.State, entry.ArrID, entry.MonitoredAt, entry.NextTry = Waiting, 0, time.Time{}, service.now()
			entry.Result = "No longer in " + a.Name
		case err == nil && grabbed:
			service.downloading(entry, a, dryRun)
		case err == nil && service.now().After(entry.ReleaseAt.Add(tries.ReleaseGrace)) && !dryRun:
			if a.Monitor(entry.ArrID, false) != nil {
				continue
			}
			entry.MonitoredAt = time.Time{}
			service.failed(entry, "Nothing came within 14 days of its release")
		default:
			continue
		}
		entry.UpdatedAt = service.now()
	}
}

// historyEvent is a title that left the libraries, or the waiting list
// unfinished: not taken again from Overfin's suggestions before
// tries.Cooldown.
type historyEvent struct {
	At    time.Time `json:"at"`
	Why   string    `json:"why"`
	Title string    `json:"title,omitempty"`
}

// history is the titles in their cooldown, by entry ID; older events are
// dropped.
func (service *Service) history() (map[string]historyEvent, error) {
	out := map[string]historyEvent{}
	raw, err := service.inv.Store().Meta(historyKey)
	if err != nil || raw == "" {
		return out, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("read the adding history: %w", err)
	}
	for id, event := range out {
		if service.now().Sub(event.At) >= tries.Cooldown {
			delete(out, id)
		}
	}
	return out, nil
}

func (service *Service) saveHistory(history map[string]historyEvent) error {
	encoded, err := json.Marshal(history)
	if err != nil {
		return err
	}
	return service.inv.Store().SetMeta(historyKey, string(encoded))
}

func (service *Service) load() ([]Entry, error) {
	database := service.inv.Store()
	if database == nil {
		return nil, fmt.Errorf("no database")
	}
	raw, err := database.Meta(storeKey)
	if err != nil || raw == "" {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("read the waiting list: %w", err)
	}
	for i := range entries {
		// Sources of before Overfin was the only one (TMDB's trending and
		// popular through Overfin, import lists): Overfin's now, so its next
		// reading gives their seasons, or takes them out of the list.
		switch entries[i].Source {
		case "discovery", "list":
			entries[i].Source = FromOverfin
		}
	}
	return entries, nil
}

func (service *Service) save(entries []Entry) error {
	database := service.inv.Store()
	if database == nil {
		return fmt.Errorf("no database")
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return database.SetMeta(storeKey, string(encoded))
}
