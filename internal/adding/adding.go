// Package adding brings titles into the libraries: it keeps a waiting list
// of titles to add, fed by Overfin's suggestions and the import lists of
// Radarr and Sonarr, and tries
// them one by one. A try adds the title, tagged, searches for it, and keeps
// it only if a download started; otherwise the title leaves Radarr or
// Sonarr at once and waits for its next try. See the ROADMAP's "Managed
// mode".
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
	"github.com/overfin/overtide/internal/services/radarr"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// Tag marks, in Radarr and Sonarr, the titles Overtide manages.
const Tag = "overtide"

// RunEvery is how often the titles are tried, and RunFor how long one run
// goes on trying titles, one search at a time: under RunEvery, so runs
// never overlap. What's left is tried by the next run. ReadEvery is how
// often Overfin's suggestions and the import lists are read (Read).
const (
	RunEvery  = 15 * time.Minute
	RunFor    = 14 * time.Minute
	ReadEvery = time.Hour
)

const storeKey = "adding.waiting"

// Source is where a title in the waiting list comes from. Requests go
// first, then Overfin's suggestions (its picks and, by the discovery
// share, TMDB's trending and popular titles), then the rest (import
// lists).
type Source string

const (
	FromRequest Source = "request"
	FromOverfin Source = "overfin"
	// FromDiscovery: TMDB's trending and popular titles, through Overfin,
	// whatever their value.
	FromDiscovery Source = "discovery"
	FromList      Source = "list"
	// HandedOver: in Radarr or Sonarr without files, handed over by the
	// operator (HandOver).
	HandedOver Source = "handed over"
)

func (source Source) rank() int {
	switch source {
	case FromRequest:
		return 0
	case FromOverfin, FromDiscovery:
		return 1
	}
	return 2
}

// State is where a title stands.
type State string

const (
	// Waiting: to be tried at NextTry.
	Waiting State = "waiting"
	// NotReleased: not out yet; tried once it is (NextTry).
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
	// From names the list it came from.
	From   string `json:"from"`
	ListID int    `json:"listId"`
	// Position is its place in its list, which orders a source's titles.
	Position int   `json:"position"`
	State    State `json:"state"`
	// Ours is true once Overtide added it, or the operator handed it over.
	Ours bool `json:"ours,omitempty"`
	// ArrID is its ID in Radarr or Sonarr while it's there.
	ArrID     int       `json:"arrId,omitempty"`
	Tries     int       `json:"tries"`
	NextTry   time.Time `json:"nextTry,omitempty"`
	Result    string    `json:"result,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func movieKey(tmdbID int) string  { return fmt.Sprintf("movie:%d", tmdbID) }
func seriesKey(tvdbID int) string { return fmt.Sprintf("series:%d", tvdbID) }

// Service keeps the waiting list and tries its titles.
type Service struct {
	inv *inventory.Service
	// mu makes runs, adoptions and reads take turns.
	mu  sync.Mutex
	now func() time.Time
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
	sortForTries(entries, service.inv.Config().Adding.Discovery())
	return entries, nil
}

// HandOver hands every title Radarr and Sonarr have without files, and
// that isn't Overtide's, over to Overtide: from its next try, each is
// handled as one Overtide added. It returns how many it handed over.
func (service *Service) HandOver() (int, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	entries, err := service.load()
	if err != nil {
		return 0, err
	}
	at := map[string]int{}
	for i, entry := range entries {
		at[entry.ServiceID+"|"+entry.Key] = i
	}
	now := service.now()
	handed := 0
	items, _, _ := service.inv.Snapshot()
	for _, m := range withoutFiles(items, entries) {
		key := titleKey(m)
		external := m.TMDBID
		if m.Type == model.Series {
			external = m.TVDBID
		}
		entry := Entry{Key: key, Kind: m.Type, ServiceID: m.ServiceID, ExternalID: external, Title: m.Title, Year: m.Year, Source: HandedOver, From: "Handed over", State: Waiting, Ours: true, ArrID: m.SourceID, NextTry: now, Result: "Handed over to Overtide", UpdatedAt: now}
		// One only waiting from a list becomes the handed-over title.
		if i, there := at[m.ServiceID+"|"+key]; there {
			entries[i] = entry
		} else {
			entries = append(entries, entry)
		}
		handed++
	}
	if handed == 0 {
		return 0, fmt.Errorf("no title to hand over")
	}
	return handed, service.save(entries)
}

// WithoutFiles counts the titles Radarr and Sonarr have without files that
// aren't Overtide's: what HandOver would take.
func (service *Service) WithoutFiles() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	entries, err := service.load()
	if err != nil {
		return 0
	}
	items, _, _ := service.inv.Snapshot()
	return len(withoutFiles(items, entries))
}

// withoutFiles lists the titles without files that aren't Overtide's:
// neither tagged nor already in the waiting list as its own (a handed-over
// title carries the tag only from its first live try).
func withoutFiles(items []model.Media, entries []Entry) []model.Media {
	ours := map[string]bool{}
	for _, entry := range entries {
		if entry.Ours {
			ours[entry.ServiceID+"|"+entry.Key] = true
		}
	}
	var out []model.Media
	for _, m := range items {
		key := titleKey(m)
		if m.SizeBytes == 0 && !hasTag(m.Tags, Tag) && key != "" && !ours[m.ServiceID+"|"+key] {
			out = append(out, m)
		}
	}
	return out
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

// Read is the reading task: it brings the import lists' titles and
// Overfin's suggestions into the waiting list.
func (service *Service) Read(ctx context.Context) error {
	cfg := service.inv.Config()
	if cfg.Adding.Mode != config.AddingDryRun && cfg.Adding.Mode != config.AddingOn {
		return nil
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	entries, err := service.load()
	if err != nil {
		return err
	}
	if entries, err = service.readLists(ctx, cfg, entries); err != nil {
		return err
	}
	return service.save(service.readOverfin(ctx, cfg, entries))
}

// Run is the trying task: it follows what's downloading, then tries the
// titles whose turn it is.
func (service *Service) Run(ctx context.Context) error {
	cfg := service.inv.Config()
	mode := cfg.Adding.Mode
	if mode != config.AddingDryRun && mode != config.AddingOn {
		return nil
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	entries, err := service.load()
	if err != nil {
		return err
	}
	entries = service.match(entries)
	service.follow(ctx, entries)
	sortForTries(entries, cfg.Adding.Discovery())
	stopAt := service.now().Add(RunFor)
	for i := range entries {
		if ctx.Err() != nil || service.now().After(stopAt) {
			break
		}
		entry := &entries[i]
		if (entry.State != Waiting && entry.State != NotReleased) || entry.NextTry.After(service.now()) {
			continue
		}
		service.try(ctx, cfg, entry, mode == config.AddingDryRun)
		entry.UpdatedAt = service.now()
		// Saved after each try: a try changes Radarr or Sonarr, and what
		// it did must not be lost if the next one stops the run.
		if err := service.save(entries); err != nil {
			return err
		}
	}
	return service.save(entries)
}

// sortForTries orders the waiting list: by source (requests first), then
// by place in its list; Overfin's suggestions mixed by the discovery share
// (interleave).
func sortForTries(entries []Entry, share int) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Source.rank() != entries[j].Source.rank() {
			return entries[i].Source.rank() < entries[j].Source.rank()
		}
		if entries[i].From != entries[j].From {
			return entries[i].From < entries[j].From
		}
		return entries[i].Position < entries[j].Position
	})
	var picks, discovered []Entry
	start := -1
	for i, entry := range entries {
		if entry.Source.rank() != 1 {
			continue
		}
		if start < 0 {
			start = i
		}
		if entry.Source == FromDiscovery {
			discovered = append(discovered, entry)
		} else {
			picks = append(picks, entry)
		}
	}
	if start >= 0 {
		copy(entries[start:], interleave(picks, discovered, share))
	}
}

// match drops from the waiting list the titles already in Radarr or
// Sonarr that aren't Overtide's: the lists are only a source of titles to
// add. One there carrying Overtide's tag is Overtide's, followed from then
// on (follow). Overtide's own titles aren't matched here: the inventory may
// not have caught up with a title just added.
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
		m, there := present[entry.ServiceID+"|"+entry.Key]
		switch {
		case entry.Ours:
		case !there:
			// A state the waiting list no longer has (saved by an older
			// version) waits for a try like any new title.
			if entry.State != Waiting && entry.State != NotReleased {
				entry.State, entry.NextTry = Waiting, service.now()
			}
		case hasTag(m.Tags, Tag):
			entry.Ours, entry.ArrID, entry.State = true, m.SourceID, Downloading
		default:
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// follow asks Radarr and Sonarr about Overtide's titles that are
// downloading: imported once they have files, waiting for another try if
// they left.
func (service *Service) follow(ctx context.Context, entries []Entry) {
	for i := range entries {
		entry := &entries[i]
		if !entry.Ours || entry.ArrID == 0 || entry.State != Downloading {
			continue
		}
		var hasFiles, gone bool
		var err error
		if entry.Kind == model.Series {
			client, clientErr := service.inv.SonarrClient(entry.ServiceID)
			if clientErr != nil {
				continue
			}
			hasFiles, err = client.WithContext(ctx).SeriesHasFiles(entry.ArrID)
			gone = sonarr.IsNotFound(err)
		} else {
			client, clientErr := service.inv.RadarrClient(entry.ServiceID)
			if clientErr != nil {
				continue
			}
			hasFiles, err = client.WithContext(ctx).MovieHasFile(entry.ArrID)
			gone = radarr.IsNotFound(err)
		}
		switch {
		case gone:
			entry.State, entry.ArrID, entry.NextTry = Waiting, 0, service.now()
			entry.Result = "No longer in " + serviceType(entry.Kind)
		case err == nil && hasFiles:
			entry.State, entry.Result = Imported, "Imported"
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

func serviceType(kind model.MediaType) string {
	if kind == model.Series {
		return "Sonarr"
	}
	return "Radarr"
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
