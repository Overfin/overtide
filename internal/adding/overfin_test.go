package adding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/store"
)

// Overfin's suggestions join the waiting list in Overfin's order: films
// to the first Radarr, series (with a TheTVDB ID) to the first Sonarr, a
// series once with its seasons; a title Overfin stops suggesting leaves,
// unless it's moving, and a request keeps its place.
func TestReadOverfin(t *testing.T) {
	answer := `{"titles":[{"kind":"movie","name":"Hot","tmdbId":"4","score":20},{"kind":"movie","name":"Pick","tmdbId":"1","score":80},{"kind":"season","name":"Show","tmdbId":"2","tvdbId":"22","season":1,"score":70},{"kind":"season","name":"Show","tmdbId":"2","tvdbId":"22","season":2,"score":75},{"kind":"season","name":"No TVDB","tmdbId":"3","season":1,"score":60}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list") != "suggestions" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(answer))
	}))
	defer srv.Close()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr"}, {ID: "r2", Type: "radarr"}, {ID: "s", Type: "sonarr"}}}
	cfg.Overfin = config.Connection{URL: srv.URL, APIKey: "ofapp_k"}
	service := New(inventory.New(cfg, database))
	service.now = func() time.Time { return *day("2026-10-25") }

	entries := []Entry{
		{Key: movieKey(9), ServiceID: "r", Source: FromOverfin, State: Waiting},
		{Key: movieKey(8), ServiceID: "r", Source: FromOverfin, State: Downloading},
		{Key: movieKey(1), ServiceID: "r", Source: FromRequest},
	}
	entries = service.readOverfin(context.Background(), cfg, entries, map[string]historyEvent{})
	got := map[string]Entry{}
	for _, entry := range entries {
		got[entry.ServiceID+"|"+entry.Key] = entry
	}
	if len(entries) != 4 {
		t.Fatalf("%d entries: %+v", len(entries), entries)
	}
	if e := got["r|movie:4"]; e.Source != FromOverfin || e.Title != "Hot" || e.Position != 0 || e.State != Waiting {
		t.Errorf("the first suggestion: %+v", e)
	}
	if e := got["r|movie:1"]; e.Source != FromRequest {
		t.Errorf("the request: %+v", e)
	}
	if e := got["s|series:22"]; e.Source != FromOverfin || e.ExternalID != 22 || e.Position != 2 {
		t.Errorf("the series: %+v", e)
	}
	if _, ok := got["r|movie:8"]; !ok {
		t.Error("a title downloading left")
	}
	if _, ok := got["r|movie:9"]; ok {
		t.Error("a title no longer suggested stayed")
	}
}

// Titles from the sources of before Overfin was the only one count as
// Overfin's: its reading gives a series it still suggests its seasons, and
// takes out what it no longer suggests, except what's moving.
func TestLegacySourcesBecomeOverfins(t *testing.T) {
	answer, _ := json.Marshal(map[string]any{"titles": []map[string]any{
		{"kind": "season", "name": "Show", "tmdbId": "900", "tvdbId": "99", "season": 1, "score": 70},
	}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(answer) }))
	defer srv.Close()
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr"}, {ID: "s", Type: "sonarr"}}}
	cfg.Overfin = config.Connection{URL: srv.URL, APIKey: "ofapp_k"}
	service := newTestService(t, cfg, *day("2026-10-30"))
	old := []Entry{
		{Key: seriesKey(99), Kind: "series", ServiceID: "s", Source: "discovery", State: Waiting},
		{Key: seriesKey(98), Kind: "series", ServiceID: "s", Source: "discovery", State: Waiting},
		{Key: movieKey(7), Kind: "movie", ServiceID: "r", Source: "list", State: Downloading, ArrID: 3},
	}
	if err := service.save(old); err != nil {
		t.Fatal(err)
	}
	entries, err := service.load()
	if err != nil {
		t.Fatal(err)
	}
	entries = service.readOverfin(context.Background(), cfg, entries, nil)
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Key] = e
	}
	if e := got[seriesKey(99)]; e.Source != FromOverfin || len(e.Seasons) != 1 || e.Seasons[0] != 1 {
		t.Errorf("series still suggested: %+v", e)
	}
	if _, there := got[seriesKey(98)]; there {
		t.Errorf("series no longer suggested still waits")
	}
	if e := got[movieKey(7)]; e.State != Downloading || e.Source != FromOverfin {
		t.Errorf("film downloading: %+v", e)
	}
}
