package adding

import (
	"context"
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
// series once; a title Overfin stops suggesting leaves, unless it's
// Overtide's, and a request keeps its place.
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
		{Key: movieKey(9), ServiceID: "r", Source: FromOverfin, Position: 0},
		{Key: movieKey(8), ServiceID: "r", Source: FromOverfin, Ours: true},
		{Key: movieKey(1), ServiceID: "r", Source: FromRequest},
	}
	entries = service.readOverfin(context.Background(), cfg, entries)
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
		t.Error("Overtide's own title left")
	}
	if _, ok := got["r|movie:9"]; ok {
		t.Error("a title no longer suggested stayed")
	}
}
