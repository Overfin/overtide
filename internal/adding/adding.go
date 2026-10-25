// Package adding brings titles into the libraries: it keeps a waiting list
// of titles to add, fed by the import lists of Radarr and Sonarr, and tries
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

// TriesPerRun is how many titles one run tries at most: each try is a
// search on the indexers.
const TriesPerRun = 5

const storeKey = "adding.waiting"

// Source is where a title in the waiting list comes from. Requests go
// first, then Overfin's own picks, then the rest (import lists).
type Source string

const (
	FromRequest Source = "request"
	FromOverfin Source = "overfin"
	FromList    Source = "list"
)

func (source Source) rank() int {
	switch source {
	case FromRequest:
		return 0
	case FromOverfin:
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
	// InLibrary: in Radarr or Sonarr with files, not Overtide's.
	InLibrary State = "in library"
	// WantedElsewhere: in Radarr or Sonarr without files, not Overtide's
	// (added by hand, or by a list on its own). The operator can hand it
	// over (Adopt).
	WantedElsewhere State = "wanted elsewhere"
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
	sortForTries(entries)
	return entries, nil
}

// Adopt hands a title that's in Radarr or Sonarr without files over to
// Overtide: from its next try, it's handled as one Overtide added. With
// key "", every such title is handed over.
func (service *Service) Adopt(key string) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	entries, err := service.load()
	if err != nil {
		return err
	}
	handed := 0
	for i := range entries {
		entry := &entries[i]
		if key != "" && entry.Key != key {
			continue
		}
		if entry.State != WantedElsewhere {
			if key != "" {
				return fmt.Errorf("%s isn't waiting in Radarr or Sonarr without files", entry.Title)
			}
			continue
		}
		entry.Ours, entry.State, entry.NextTry = true, Waiting, service.now()
		entry.Result, entry.UpdatedAt = "Handed over to Overtide", service.now()
		handed++
	}
	if handed == 0 {
		return fmt.Errorf("no title to hand over")
	}
	return service.save(entries)
}

// Run is the adding task: it reads the import lists, follows what's
// downloading, then tries the titles whose turn it is.
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
	entries, err = service.readLists(ctx, cfg, entries)
	if err != nil {
		return err
	}
	service.match(entries)
	service.follow(ctx, entries)
	sortForTries(entries)
	tried := 0
	for i := range entries {
		if tried == TriesPerRun || ctx.Err() != nil {
			break
		}
		entry := &entries[i]
		if (entry.State != Waiting && entry.State != NotReleased) || entry.NextTry.After(service.now()) {
			continue
		}
		tried++
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
// by place in its list.
func sortForTries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Source.rank() != entries[j].Source.rank() {
			return entries[i].Source.rank() < entries[j].Source.rank()
		}
		if entries[i].From != entries[j].From {
			return entries[i].From < entries[j].From
		}
		return entries[i].Position < entries[j].Position
	})
}

// match sets, from the inventory, the state of the titles already in
// Radarr or Sonarr that aren't Overtide's: in the library with files, or
// wanted elsewhere without. One carrying Overtide's tag becomes Overtide's,
// followed from then on (follow). Overtide's own titles aren't matched
// here: the inventory may not have caught up with a title just added.
func (service *Service) match(entries []Entry) {
	items, _, _ := service.inv.Snapshot()
	present := map[string]model.Media{}
	for _, m := range items {
		switch {
		case m.Type == model.Movie && m.TMDBID > 0:
			present[m.ServiceID+"|"+movieKey(m.TMDBID)] = m
		case m.Type == model.Series && m.TVDBID > 0:
			present[m.ServiceID+"|"+seriesKey(m.TVDBID)] = m
		}
	}
	now := service.now()
	for i := range entries {
		entry := &entries[i]
		if entry.Ours {
			continue
		}
		m, there := present[entry.ServiceID+"|"+entry.Key]
		switch {
		case there && hasTag(m.Tags, Tag):
			entry.Ours, entry.ArrID, entry.State = true, m.SourceID, Downloading
		case there && m.SizeBytes > 0:
			entry.State, entry.ArrID = InLibrary, m.SourceID
		case there:
			entry.State, entry.ArrID = WantedElsewhere, m.SourceID
		case entry.State == InLibrary || entry.State == WantedElsewhere:
			entry.State, entry.ArrID, entry.NextTry = Waiting, 0, now
		}
	}
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
