// Package adding brings titles into the libraries: it keeps a waiting list
// of titles to add, requests first, then what Overfin suggests, and tries
// them one by one. Overfin gives opinions; Overtide decides. A title goes
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
	"strings"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
)

// Tag marks, in Radarr and Sonarr, the titles Overtide manages.
const Tag = model.ManagedTag

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
	// managedKey records when managed mode first tagged every title.
	managedKey = "adding.managed_at"
)

// Source is where a title in the waiting list comes from: requests always
// go first, then Overfin's suggestions, in Overfin's order.
type Source string

const (
	FromRequest Source = "request"
	FromOverfin Source = "overfin"
)

func (source Source) rank() int {
	if source == FromRequest {
		return 0
	}
	return 1
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
	// Downloading: a try started a download; followed until imported.
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
	ArrID   int       `json:"arrId,omitempty"`
	Tries   int       `json:"tries"`
	NextTry time.Time `json:"nextTry,omitempty"`
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

// Run is the trying task: when managed mode is first on, it tags every
// title; then it follows what's moving, applies the lifecycle, and tries
// the titles whose turn it is.
func (service *Service) Run(ctx context.Context) error {
	cfg := service.inv.Config()
	mode := cfg.Adding.Mode
	if mode != config.AddingDryRun && mode != config.AddingOn {
		return nil
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if mode == config.AddingOn {
		if err := service.manageAll(ctx, cfg); err != nil {
			return err
		}
	}
	entries, err := service.load()
	if err != nil {
		return err
	}
	entries = service.match(entries)
	service.follow(ctx, entries, mode == config.AddingDryRun)
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
		searches += service.try(ctx, cfg, entry, mode == config.AddingDryRun)
		entry.UpdatedAt = service.now()
		// Saved after each try: a try changes Radarr or Sonarr, and what
		// it did must not be lost if the next one stops the run.
		if err := service.save(entries); err != nil {
			return err
		}
	}
	return service.save(entries)
}

// manageAll tags every title Radarr and Sonarr have, the first time
// managed mode is on: from then on, Overtide manages them all.
func (service *Service) manageAll(ctx context.Context, cfg config.Config) error {
	if done, err := service.inv.Store().Meta(managedKey); err != nil || done != "" {
		return err
	}
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			return err
		}
		client = client.WithContext(ctx)
		tagID, err := client.EnsureTag(Tag)
		if err != nil {
			return fmt.Errorf("tag Radarr's films: %w", err)
		}
		if _, err := client.TagAll(tagID); err != nil {
			return fmt.Errorf("tag Radarr's films: %w", err)
		}
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return err
		}
		client = client.WithContext(ctx)
		tagID, err := client.EnsureTag(Tag)
		if err != nil {
			return fmt.Errorf("tag Sonarr's series: %w", err)
		}
		if _, err := client.TagAll(tagID); err != nil {
			return fmt.Errorf("tag Sonarr's series: %w", err)
		}
	}
	return service.inv.Store().SetMeta(managedKey, service.now().UTC().Format(time.RFC3339))
}

// managed reports whether a title in Radarr or Sonarr is Overtide's: it
// carries the tag, or managed mode hasn't tagged everything yet (then all
// will be, so a dry run counts them as Overtide's too).
func (service *Service) managed(m model.Media) bool {
	if hasTag(m.Tags, Tag) {
		return true
	}
	done, err := service.inv.Store().Meta(managedKey)
	return err == nil && done == ""
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
	for i := range entries {
		entry := &entries[i]
		monitoredForRelease := entry.State == NotReleased && !entry.MonitoredAt.IsZero()
		if entry.ArrID == 0 || (entry.State != Downloading && !monitoredForRelease) {
			continue
		}
		a, err := service.arrFor(ctx, entry)
		if err != nil {
			continue
		}
		if entry.State == Downloading {
			hasFiles, err := a.hasFiles(entry.ArrID)
			switch {
			case a.gone(err):
				entry.State, entry.ArrID, entry.NextTry = Waiting, 0, service.now()
				entry.Result = "No longer in " + a.name
			case err == nil && hasFiles:
				entry.State, entry.Result = Imported, "Imported"
			default:
				continue
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
			entry.State, entry.Result = Downloading, "Downloading"
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

func hasTag(tags []string, wanted string) bool {
	for _, tag := range tags {
		if strings.EqualFold(tag, wanted) {
			return true
		}
	}
	return false
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
