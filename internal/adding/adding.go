// Package adding brings titles into the libraries: it keeps a waiting list
// of titles to add, requests first, then what Overfin suggests, and tries
// them one by one. A try adds the title, tagged, searches for it, and keeps
// it only if a download started; otherwise the title leaves Radarr or
// Sonarr at once and waits for its next try. Import lists are the
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
	"github.com/overfin/overtide/internal/services/radarr"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// Tag marks, in Radarr and Sonarr, the titles Overtide manages.
const Tag = "overtide"

// RunEvery is how often the titles are tried, and RunFor how long one run
// goes on trying titles, one search at a time: under RunEvery, so runs
// never overlap. What's left is tried by the next run. ReadEvery is how
// often Overfin's suggestions are read (ReadOverfin).
const (
	RunEvery  = 15 * time.Minute
	RunFor    = 14 * time.Minute
	ReadEvery = time.Hour
)

const storeKey = "adding.waiting"

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
	// Position is its place in its source's order.
	Position int   `json:"position"`
	State    State `json:"state"`
	// Ours is true once Overtide added it (or found it tagged as its own).
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
	// mu makes runs and reads take turns.
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

// ReadOverfin is the Overfin suggestions task: it brings what Overfin
// suggests adding into the waiting list, in Overfin's order.
func (service *Service) ReadOverfin(ctx context.Context) error {
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
	sortForTries(entries)
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

// match drops from the waiting list the titles already in Radarr or
// Sonarr that aren't Overtide's: the operator has them already. One there
// carrying Overtide's tag is Overtide's, followed from then on (follow).
// Overtide's own titles aren't matched here: the inventory may not have
// caught up with a title just added.
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
