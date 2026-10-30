// Package adding brings titles into the libraries: it keeps a waiting list
// of titles to add, what Overfin suggests (requests, once Overfin keeps
// them, will go first), and tries them one by one. Overfin gives opinions; Overtide decides. A title goes
// in Radarr or Sonarr once, unmonitored and tagged, and is never deleted
// from them: a try monitors it and searches for it, and if nothing started
// it's unmonitored again until its next try. Import lists are the
// operator's business: Overtide never reads them. See the ROADMAP's
// "Managed mode".
package adding

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
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

// The waiting list's lifecycle and the history's cooldown, fixed for now
// (see the ROADMAP's "Managed mode").
const (
	// WaitingMax is how many suggestions may wait; when full, no new one
	// is taken.
	WaitingMax = 200
	// MaxTries and MaxAge: a suggestion leaves after that many tries that
	// found nothing, or that long in the list.
	MaxTries = 5
	MaxAge   = 60 * 24 * time.Hour
	// Cooldown: a title that left the libraries, or the list unfinished,
	// isn't taken from Overfin's suggestions for that long.
	Cooldown = 183 * 24 * time.Hour
	// ReleaseWindow: a title coming out within it is monitored without a
	// search, so Radarr's or Sonarr's RSS catches the release, until
	// ReleaseGrace after the release.
	ReleaseWindow = 2 * 24 * time.Hour
	ReleaseGrace  = 14 * 24 * time.Hour
	// SearchesPerRun paces the indexers: a run stops after that many
	// searches (a series' seasons each count), so at most 12 an hour.
	SearchesPerRun = 3
)

const (
	storeKey     = "adding.waiting"
	historyKey   = "adding.history"
	withFilesKey = "adding.with_files"
	// tagsKey records that the legacy tag was replaced by the tags that
	// say where a title came from.
	tagsKey = "adding.tags_v2"
)

// Source is where a title in the waiting list comes from: requests always
// go first, then the titles "Manage all" found waiting in Radarr, then
// Overfin's suggestions, in Overfin's order.
type Source string

const (
	FromRequest Source = "request"
	FromLibrary Source = "library"
	FromOverfin Source = "overfin"
)

func (source Source) rank() int {
	switch source {
	case FromRequest:
		return 0
	case FromLibrary:
		return 1
	}
	return 2
}

// State is where a title stands.
type State string

const (
	// Waiting: to be tried at NextTry.
	Waiting State = "waiting"
	// NotReleased: not out yet. Unmonitored (ArrID 0 or not monitored) it
	// waits for ReleaseWindow before its release (NextTry); monitored
	// (MonitoredAt set), follow looks for the grab until ReleaseGrace after
	// the release.
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

// Service keeps the waiting list and tries its titles.
type Service struct {
	inv *inventory.Service
	// mu makes runs and reads take turns.
	mu  sync.Mutex
	now func() time.Time
	// disk replaces diskStop in tests.
	disk func(cfg config.Config, rootFolder string, dryRun bool) string
	// preview is the wanted episodes a dry run of takeAll found, which it
	// doesn't save: what replaceSeasons counts in dry run.
	preview map[string]*sonarrState
}

func New(inv *inventory.Service) *Service {
	return &Service{inv: inv, now: time.Now}
}

// Entries returns the waiting list in the order titles are tried.
func (service *Service) Entries() ([]Entry, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
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
// what's moving, applies the lifecycle, tries the titles whose turn it is,
// and with what's left of the run's searches, the wanted episodes.
func (service *Service) Run(ctx context.Context) error {
	cfg := service.inv.Config()
	mode := cfg.Adding.Mode
	if mode != config.AddingDryRun && mode != config.AddingOn {
		return nil
	}
	dryRun := mode == config.AddingDryRun
	service.mu.Lock()
	defer service.mu.Unlock()
	entries, err := service.load()
	if err != nil {
		return err
	}
	if !dryRun {
		if err := service.retag(ctx, cfg, entries); err != nil {
			return err
		}
	}
	if cfg.Adding.ManagesAll() {
		if entries, err = service.takeAll(ctx, cfg, entries, dryRun); err != nil {
			return err
		}
	}
	entries = service.match(entries)
	service.follow(ctx, entries, dryRun)
	if entries, err = service.expire(entries); err != nil {
		return err
	}
	sortForTries(entries)
	stopAt := service.now().Add(RunFor)
	searches := 0
	for i := range entries {
		if ctx.Err() != nil || service.now().After(stopAt) || searches >= SearchesPerRun {
			break
		}
		entry := &entries[i]
		if !entry.waiting() || entry.NextTry.After(service.now()) {
			continue
		}
		// A series whose seasons would go past the limit waits for the next
		// run, unless it's the run's first title (it would never fit).
		if searches > 0 && searches+entry.searches() > SearchesPerRun {
			continue
		}
		searches += service.try(ctx, cfg, entry, dryRun)
		entry.UpdatedAt = service.now()
		// Saved after each try: a try changes Radarr or Sonarr, and what
		// it did must not be lost if the next one stops the run.
		if err := service.save(entries); err != nil {
			return err
		}
	}
	if err := service.save(entries); err != nil {
		return err
	}
	if !cfg.Adding.ManagesAll() {
		return nil
	}
	used, err := service.replaceSeasons(ctx, cfg, SearchesPerRun-searches, dryRun)
	if err != nil {
		return err
	}
	searches += used
	if searches < SearchesPerRun && !service.now().After(stopAt) {
		return service.searchEpisodes(ctx, cfg, SearchesPerRun-searches, dryRun)
	}
	return nil
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

// expire takes out the suggestions at the end of their lifecycle: MaxTries
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
			case entry.Tries >= MaxTries:
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
		case !there && entry.Source == FromLibrary:
			continue // taken from Radarr, and gone from it since
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
// downloading once grabbed, or, nothing grabbed ReleaseGrace after the
// release, unmonitored and counted as a try that found nothing. A title
// that left Radarr or Sonarr waits for another try.
func (service *Service) follow(ctx context.Context, entries []Entry, dryRun bool) {
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
			if a.monitor(entry.ArrID, false) == nil {
				entry.Unmonitored = true
				entry.UpdatedAt = service.now()
			}
		}
		if entry.State == Imported {
			continue
		}
		if entry.State == Downloading {
			hasFiles, err := a.hasFiles(entry.ArrID)
			switch {
			case a.gone(err):
				entry.State, entry.ArrID, entry.NextTry = Waiting, 0, service.now()
				entry.Result = "No longer in " + a.name
			case err != nil:
				continue
			case hasFiles:
				entry.State, entry.Result = Imported, "Imported"
			default:
				// Unmonitored, it isn't downloaded again on its own: a
				// download that left the queue without its files goes back
				// in line.
				queued, err := a.queued(entry.ArrID)
				if err != nil || queued {
					continue
				}
				if hasFiles, err = a.hasFiles(entry.ArrID); err != nil {
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
		grabbed, err := a.grabbed(entry.ArrID, entry.MonitoredAt)
		switch {
		case a.gone(err):
			entry.State, entry.ArrID, entry.MonitoredAt, entry.NextTry = Waiting, 0, time.Time{}, service.now()
			entry.Result = "No longer in " + a.name
		case err == nil && grabbed:
			service.downloading(entry, a, dryRun)
		case err == nil && service.now().After(entry.ReleaseAt.Add(ReleaseGrace)) && !dryRun:
			if a.monitor(entry.ArrID, false) != nil {
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

// retryDelay is the wait after the tries-th try found nothing: a day,
// doubled each time, at most 30 days.
func retryDelay(tries int) time.Duration {
	days := 1
	for i := 1; i < tries && days < 30; i++ {
		days *= 2
	}
	if days > 30 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

// historyEvent is a title that left the libraries, or the waiting list
// unfinished: not taken again from Overfin's suggestions before Cooldown.
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
		if service.now().Sub(event.At) >= Cooldown {
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

// recordLeaves adds to the history every title that had files at the last
// check and has none now, or left Radarr or Sonarr, whoever removed it.
func (service *Service) recordLeaves(history map[string]historyEvent) error {
	items, _, err := service.inv.Snapshot()
	if err != nil || len(items) == 0 {
		return nil // the inventory isn't there yet: compare next time
	}
	now := map[string]string{}
	for _, m := range items {
		if key := titleKey(m); key != "" && m.SizeBytes > 0 {
			now[m.ServiceID+"|"+key] = m.Title
		}
	}
	var before map[string]string
	if raw, err := service.inv.Store().Meta(withFilesKey); err != nil {
		return err
	} else if raw != "" {
		if err := json.Unmarshal([]byte(raw), &before); err != nil {
			return fmt.Errorf("read the titles with files: %w", err)
		}
	}
	for id, title := range before {
		if _, still := now[id]; !still {
			history[id] = historyEvent{At: service.now(), Why: "Left the libraries", Title: title}
		}
	}
	encoded, err := json.Marshal(now)
	if err != nil {
		return err
	}
	return service.inv.Store().SetMeta(withFilesKey, string(encoded))
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
