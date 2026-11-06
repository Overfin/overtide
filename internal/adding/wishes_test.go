package adding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/store"
)

// fakeWishes is Overfin for wishes: the list in turn order, and the
// statuses told back (a title no one wishes for answers 404).
type fakeWishes struct {
	mu     sync.Mutex
	list   []map[string]any
	wished map[string]bool // "movie 1", "season 2/3"
	told   []string
}

func (f *fakeWishes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/apps/v1/wishes":
		json.NewEncoder(w).Encode(map[string]any{"titles": f.list})
	case "/apps/v1/wishes/status":
		var body struct {
			Kind, TmdbId, State, NextTry, ReleaseDate string
			Season                                    *int
		}
		json.NewDecoder(r.Body).Decode(&body)
		key := body.Kind + " " + body.TmdbId
		if body.Season != nil {
			key += "/" + string(rune('0'+*body.Season))
		}
		f.told = append(f.told, key+" "+body.State)
		if !f.wished[key] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func wishService(t *testing.T, f *fakeWishes) (*Service, config.Config) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr"}, {ID: "s", Type: "sonarr"}}}
	cfg.Overfin = config.Connection{URL: srv.URL, APIKey: "ofapp_k"}
	service := New(inventory.New(cfg, database))
	service.now = func() time.Time { return *arrfake.Day("2026-11-01") }
	return service, cfg
}

// Wishes join the waiting list first, in Overfin's turn order: a film to
// the first Radarr, a series' wished seasons as one Sonarr title; a title
// Overtide had from Overfin's suggestions becomes a wish.
func TestReadWishes(t *testing.T) {
	f := &fakeWishes{list: []map[string]any{
		{"kind": "season", "name": "Show", "tmdbId": "2", "tvdbId": "22", "season": 3},
		{"kind": "movie", "name": "Film", "tmdbId": "1"},
		{"kind": "season", "name": "Show", "tmdbId": "2", "tvdbId": "22", "season": 1},
		{"kind": "season", "name": "No TVDB", "tmdbId": "5", "season": 1},
	}}
	service, cfg := wishService(t, f)
	entries := []Entry{{Key: movieKey(1), ServiceID: "r", Source: FromOverfin, State: Waiting, Position: 7}}
	entries = service.readWishes(context.Background(), cfg, entries)
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.id()] = e
	}
	if len(entries) != 2 {
		t.Fatalf("%d entries: %+v", len(entries), entries)
	}
	if e := got["s|series:22"]; e.Source != FromRequest || e.Position != 0 || !slices.Equal(e.Seasons, []int{3, 1}) || e.WishTMDB != 2 {
		t.Errorf("the series: %+v", e)
	}
	if e := got["r|movie:1"]; e.Source != FromRequest || e.Position != 1 || e.WishTMDB != 1 {
		t.Errorf("the film, a suggestion before: %+v", e)
	}
}

// What became of a wish is told once per change, each season on its own;
// nothing in dry run; a wish no one wants any more leaves the list while
// it still waits.
func TestReportWishes(t *testing.T) {
	f := &fakeWishes{wished: map[string]bool{"movie 1": true, "season 2/1": true, "season 2/3": true}}
	service, cfg := wishService(t, f)
	ctx := context.Background()
	next := arrfake.Day("2026-11-03")
	entries := []Entry{
		{Key: movieKey(1), ServiceID: "r", Source: FromRequest, WishTMDB: 1, State: Waiting, Tries: 1, NextTry: *next},
		{Key: seriesKey(22), ServiceID: "s", Kind: "series", Source: FromRequest, WishTMDB: 2, Seasons: []int{1, 3}, State: Downloading},
		{Key: movieKey(9), ServiceID: "r", Source: FromRequest, WishTMDB: 9, State: Waiting},
		{Key: movieKey(4), ServiceID: "r", Source: FromOverfin, State: Waiting, Tries: 1},
	}
	if out := service.reportWishes(ctx, cfg, slices.Clone(entries), true); len(out) != 4 || len(f.told) != 0 {
		t.Fatalf("dry run: %d entries, told %v", len(out), f.told)
	}
	entries = service.reportWishes(ctx, cfg, entries, false)
	want := []string{"movie 1 notFound", "season 2/1 downloading", "season 2/3 downloading", "movie 9 waiting"}
	if !slices.Equal(f.told, want) {
		t.Fatalf("told %v, want %v", f.told, want)
	}
	if len(entries) != 3 {
		t.Fatalf("film 9, no longer wished, stayed: %+v", entries)
	}
	// Nothing changed: nothing told again.
	f.told = nil
	entries = service.reportWishes(ctx, cfg, entries, false)
	if len(f.told) != 0 {
		t.Fatalf("told again: %v", f.told)
	}
	// Tried again, still not found, later: told.
	entries[0].Tries, entries[0].NextTry = 2, *arrfake.Day("2026-11-05")
	service.reportWishes(ctx, cfg, entries, false)
	if !slices.Equal(f.told, []string{"movie 1 notFound"}) {
		t.Fatalf("a new try: %v", f.told)
	}
}

// A wish Overfin no longer lists is asked about again: dropped once no one
// wishes for it, kept when it's only not due.
func TestWishNoLongerListed(t *testing.T) {
	f := &fakeWishes{wished: map[string]bool{"movie 2": true}}
	service, cfg := wishService(t, f)
	ctx := context.Background()
	notDue := arrfake.Day("2026-11-09")
	entries := []Entry{
		{Key: movieKey(1), ServiceID: "r", Source: FromRequest, WishTMDB: 1, State: Waiting, Reported: "waiting"},
		{Key: movieKey(2), ServiceID: "r", Source: FromRequest, WishTMDB: 2, State: Waiting, Tries: 1, NextTry: *notDue},
	}
	entries[1].Reported = "notFound " + notDue.UTC().Format(time.RFC3339) + " "
	entries = service.reportWishes(ctx, cfg, service.readWishes(ctx, cfg, entries), false)
	if len(entries) != 1 || entries[0].Key != movieKey(2) {
		t.Fatalf("kept: %+v", entries)
	}
}
