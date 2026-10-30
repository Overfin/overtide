package adding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/store"
)

func newTestService(t *testing.T, cfg config.Config, now time.Time) *Service {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	service := New(inventory.New(cfg, database))
	service.now = func() time.Time { return now }
	return service
}

// A suggestion leaves after MaxTries tries or MaxAge in the list, into the
// history; requests and moving titles stay.
func TestExpire(t *testing.T) {
	now := *day("2026-10-25")
	service := newTestService(t, config.Config{}, now)
	entries := []Entry{
		{Key: "movie:1", ServiceID: "r", Source: FromOverfin, State: Waiting, Tries: MaxTries, AddedAt: now},
		{Key: "movie:2", ServiceID: "r", Source: FromOverfin, State: Waiting, AddedAt: now.Add(-MaxAge)},
		{Key: "movie:3", ServiceID: "r", Source: FromOverfin, State: Waiting, AddedAt: now.Add(-time.Hour)},
		{Key: "movie:4", ServiceID: "r", Source: FromRequest, State: Waiting, Tries: MaxTries, AddedAt: now.Add(-MaxAge)},
		{Key: "movie:5", ServiceID: "r", Source: FromOverfin, State: Downloading, AddedAt: now.Add(-MaxAge)},
	}
	kept, err := service.expire(entries)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, e := range kept {
		keys = append(keys, e.Key)
	}
	if strings.Join(keys, " ") != "movie:3 movie:4 movie:5" {
		t.Fatalf("kept %v", keys)
	}
	history, _ := service.history()
	if len(history) != 2 || history["r|movie:1"].Why == "" || history["r|movie:2"].Why != "Not added in 60 days" {
		t.Fatalf("history: %+v", history)
	}
	// The cooldown ends.
	service.now = func() time.Time { return now.Add(Cooldown) }
	if history, _ := service.history(); len(history) != 0 {
		t.Fatalf("after the cooldown: %+v", history)
	}
}

// Suggestions in their cooldown aren't taken, nor new ones once
// WaitingMax wait; a series keeps the seasons suggested.
func TestReadOverfinCapAndCooldown(t *testing.T) {
	var titles []map[string]any
	for i := 1; i <= WaitingMax+5; i++ {
		titles = append(titles, map[string]any{"kind": "movie", "name": "Film", "tmdbId": itoa(i), "score": 50})
	}
	titles = append([]map[string]any{
		{"kind": "season", "name": "Show", "tmdbId": "9000", "tvdbId": "99", "season": 2, "score": 70},
		{"kind": "season", "name": "Show", "tmdbId": "9000", "tvdbId": "99", "season": 3, "score": 60},
	}, titles...)
	answer, _ := json.Marshal(map[string]any{"titles": titles})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(answer) }))
	defer srv.Close()
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr"}, {ID: "s", Type: "sonarr"}}}
	cfg.Overfin = config.Connection{URL: srv.URL, APIKey: "ofapp_k"}
	service := newTestService(t, cfg, *day("2026-10-25"))
	history := map[string]historyEvent{"r|movie:1": {At: *day("2026-10-01"), Why: "Left the libraries"}}
	entries := service.readOverfin(context.Background(), cfg, nil, history)
	if len(entries) != WaitingMax {
		t.Fatalf("%d entries", len(entries))
	}
	if e := entries[0]; e.Key != "series:99" || len(e.Seasons) != 2 || e.Seasons[0] != 2 || e.Seasons[1] != 3 || e.AddedAt.IsZero() {
		t.Errorf("the series: %+v", e)
	}
	for _, e := range entries {
		if e.Key == "movie:1" {
			t.Error("a title in its cooldown was taken")
		}
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// A live try of a film: added once, unmonitored and tagged; monitored and
// searched; nothing started, unmonitored again and counted; a later try
// that starts a download unmonitors it too, so Radarr doesn't upgrade it.
func TestTryMovieMonitors(t *testing.T) {
	var calls []string
	grab := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"Film","tmdbId":5,"digitalRelease":"2026-01-01T00:00:00Z"}`))
		case r.URL.Path == "/api/v3/rootfolder":
			w.Write([]byte(`[{"path":"/data/Films"}]`))
		case r.URL.Path == "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":7}]`))
		case r.URL.Path == "/api/v3/tag" && r.Method == http.MethodGet:
			w.Write([]byte(`[{"id":3,"label":"overtide-added"}]`))
		case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodPost:
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			calls = append(calls, "add monitored="+jsonString(body["monitored"]))
			w.Write([]byte(`{"id":42}`))
		case r.URL.Path == "/api/v3/movie/42" && r.Method == http.MethodGet:
			w.Write([]byte(`{"id":42,"tags":[3],"monitored":false}`))
		case r.URL.Path == "/api/v3/movie/42" && r.Method == http.MethodPut:
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			calls = append(calls, "monitored="+jsonString(body["monitored"]))
		case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
			calls = append(calls, "search")
			w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v3/command/1":
			w.Write([]byte(`{"status":"completed"}`))
		case r.URL.Path == "/api/v3/queue/details":
			if grab {
				w.Write([]byte(`[{}]`))
			} else {
				w.Write([]byte(`[]`))
			}
		case r.URL.Path == "/api/v3/history/movie":
			w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr", URL: srv.URL, APIKey: "k"}}}
	service := newTestService(t, cfg, *day("2026-10-25"))
	service.disk = func(config.Config, string, bool) string { return "" }
	pollEvery = time.Millisecond

	entry := Entry{Key: movieKey(5), Kind: "movie", ServiceID: "r", ExternalID: 5, Source: FromOverfin, State: Waiting}
	if searches := service.try(context.Background(), cfg, &entry, false); searches != 1 {
		t.Fatalf("%d searches counted", searches)
	}
	if got := strings.Join(calls, ", "); got != "add monitored=false, monitored=true, search, monitored=false" {
		t.Fatalf("calls: %s", got)
	}
	if entry.ArrID != 42 || entry.State != Waiting || entry.Tries != 1 {
		t.Fatalf("nothing found: %+v", entry)
	}
	calls, grab = nil, true
	service.try(context.Background(), cfg, &entry, false)
	if got := strings.Join(calls, ", "); got != "monitored=true, search, monitored=false" {
		t.Fatalf("calls: %s", got)
	}
	if entry.State != Downloading || !entry.Unmonitored {
		t.Fatalf("grabbed: %+v", entry)
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
