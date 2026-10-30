package adding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
)

// Run with six films waiting: off, it asks Radarr nothing; in its dry run,
// it changes nothing there; on, it asks for 3 searches and no more.
func TestRunHonorsItsModeAndPace(t *testing.T) {
	var mu sync.Mutex
	var asked, changes []string
	searches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			changes = append(changes, r.Method+" "+r.URL.Path)
		}
		switch {
		case r.URL.Path == "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"Film","tmdbId":5,"digitalRelease":"2026-01-01T00:00:00Z"}`))
		case r.URL.Path == "/api/v3/rootfolder":
			w.Write([]byte(`[{"path":"/data/Films"}]`))
		case r.URL.Path == "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":7}]`))
		case r.URL.Path == "/api/v3/tag" && r.Method == http.MethodGet:
			w.Write([]byte(`[{"id":3,"label":"overtide"}]`))
		case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodPost:
			w.Write([]byte(`{"id":42}`))
		case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodGet:
			w.Write([]byte(`[]`))
		case strings.HasPrefix(r.URL.Path, "/api/v3/movie/") && r.Method == http.MethodGet:
			w.Write([]byte(`{"id":42,"tags":[3],"monitored":false}`))
		case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
			searches++
			w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v3/command/1":
			w.Write([]byte(`{"status":"completed"}`))
		case r.URL.Path == "/api/v3/queue/details", r.URL.Path == "/api/v3/history/movie":
			w.Write([]byte(`[]`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	pollEvery = time.Millisecond

	for _, mode := range []string{config.AddingOff, config.AddingDryRun, config.AddingOn} {
		mu.Lock()
		asked, changes, searches = nil, nil, 0
		mu.Unlock()
		cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr", URL: srv.URL, APIKey: "k"}}}
		cfg.Adding.Mode = mode
		now := *day("2026-10-25")
		service := newTestService(t, cfg, now)
		service.disk = func(config.Config, string, bool) string { return "" }
		var entries []Entry
		for i := 1; i <= 6; i++ {
			entries = append(entries, Entry{Key: movieKey(i), Kind: "movie", ServiceID: "r", ExternalID: i, Source: FromOverfin, State: Waiting, Position: i, AddedAt: now})
		}
		if err := service.save(entries); err != nil {
			t.Fatal(err)
		}
		if err := service.Run(context.Background()); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		mu.Lock()
		switch mode {
		case config.AddingOff:
			if len(asked) != 0 {
				t.Errorf("off: asked %v", asked)
			}
		case config.AddingDryRun:
			if len(changes) != 0 {
				t.Errorf("dry run: changed %v", changes)
			}
			if len(asked) == 0 {
				t.Errorf("dry run: looked nothing up")
			}
		case config.AddingOn:
			if searches != SearchesPerRun {
				t.Errorf("on: %d searches, want %d", searches, SearchesPerRun)
			}
		}
		mu.Unlock()
	}
}
