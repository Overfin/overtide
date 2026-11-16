package adding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/tries"
)

// fakeFilm is a Radarr with one film: whether it has its file, whether a
// download is in the queue, and the monitoring changes asked.
type fakeFilm struct {
	mu        sync.Mutex
	hasFile   bool
	queued    bool
	monitored []bool
}

func (f *fakeFilm) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/api/v3/movie/42" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "tags": []int{3}, "monitored": true, "hasFile": f.hasFile})
	case r.URL.Path == "/api/v3/movie/42" && r.Method == http.MethodPut:
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		f.monitored = append(f.monitored, body["monitored"] == true)
		w.Write([]byte(`{}`))
	case r.URL.Path == "/api/v3/queue/details":
		if f.queued {
			w.Write([]byte(`[{}]`))
		} else {
			w.Write([]byte(`[]`))
		}
	case r.URL.Path == "/api/v3/history/movie":
		w.Write([]byte(`[{"eventType":"grabbed","date":"2026-10-25T12:00:00Z"}]`))
	default:
		w.Write([]byte(`{}`))
	}
}

func followOne(t *testing.T, film *fakeFilm, entry Entry, dryRun bool) Entry {
	t.Helper()
	srv := httptest.NewServer(film)
	t.Cleanup(srv.Close)
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr", URL: srv.URL, APIKey: "k"}}}
	service := newTestService(t, cfg, *arrfake.Day("2026-10-26"))
	entries := []Entry{entry}
	service.follow(context.Background(), entries, dryRun)
	return entries[0]
}

func TestFollowKeepsTitlesUnmonitored(t *testing.T) {
	base := Entry{Key: movieKey(5), Kind: "movie", ServiceID: "r", ExternalID: 5, Source: FromOverfin, ArrID: 42}

	// An imported title left monitored by an older version: unmonitored
	// once, and not in dry run.
	imported := base
	imported.State = Imported
	film := &fakeFilm{hasFile: true}
	if got := followOne(t, film, imported, true); got.Unmonitored || len(film.monitored) != 0 {
		t.Errorf("dry run: %+v, asked %v", got, film.monitored)
	}
	film = &fakeFilm{hasFile: true}
	got := followOne(t, film, imported, false)
	if !got.Unmonitored || len(film.monitored) != 1 || film.monitored[0] {
		t.Errorf("imported: %+v, asked %v", got, film.monitored)
	}
	film = &fakeFilm{hasFile: true}
	if followOne(t, film, got, false); len(film.monitored) != 0 {
		t.Errorf("unmonitored again: %v", film.monitored)
	}

	downloading := base
	downloading.State, downloading.Unmonitored = Downloading, true
	// Still in the queue: it waits.
	if got := followOne(t, &fakeFilm{queued: true}, downloading, false); got.State != Downloading {
		t.Errorf("queued: %+v", got)
	}
	// Left the queue without its file: back in line, counted.
	if got := followOne(t, &fakeFilm{}, downloading, false); got.State != Waiting || got.Tries != 1 || !strings.Contains(got.Result, "stopped") {
		t.Errorf("download stopped: %+v", got)
	}
	// Imported.
	if got := followOne(t, &fakeFilm{hasFile: true}, downloading, false); got.State != Imported {
		t.Errorf("imported: %+v", got)
	}

	// Monitored for its release, then grabbed: unmonitored.
	release := base
	release.State, release.MonitoredAt, release.ReleaseAt = NotReleased, *arrfake.Day("2026-10-24"), *arrfake.Day("2026-10-25")
	film = &fakeFilm{queued: true}
	got = followOne(t, film, release, false)
	if got.State != Downloading || !got.Unmonitored || len(film.monitored) != 1 || film.monitored[0] {
		t.Errorf("grabbed on release: %+v, asked %v", got, film.monitored)
	}
}

// A title monitored for its release counts in the titles a day at once
// (Radarr's RSS may grab it any time), and its grab doesn't count again.
func TestMonitoredForReleaseCountsOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"Film","tmdbId":5,"digitalRelease":"2026-10-26T00:00:00Z"}`))
		case r.URL.Path == "/api/v3/rootfolder":
			w.Write([]byte(`[{"path":"/data/Films"}]`))
		case r.URL.Path == "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":7}]`))
		case r.URL.Path == "/api/v3/tag":
			w.Write([]byte(`[{"id":3,"label":"overtide-added"}]`))
		case r.URL.Path == "/api/v3/movie/42" && r.Method == http.MethodGet:
			w.Write([]byte(`{"id":42,"tags":[3],"monitored":false}`))
		case r.URL.Path == "/api/v3/movie/42":
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr", URL: srv.URL, APIKey: "k"}}}
	service := newTestService(t, cfg, *arrfake.Day("2026-10-25"))
	service.disk = func(config.Config, string, bool) string { return "" }
	entry := Entry{Key: movieKey(5), Kind: "movie", ServiceID: "r", ExternalID: 5, Source: FromOverfin, State: Waiting, ArrID: 42}
	service.try(context.Background(), cfg, &entry, false)
	if entry.State != NotReleased || entry.MonitoredAt.IsZero() {
		t.Fatalf("not monitored for its release: %+v", entry)
	}
	if times, _ := service.started(""); len(times) != 1 {
		t.Fatalf("monitored for its release: %d counted, want 1", len(times))
	}
	service.downloading(&entry, tries.Title{Monitor: func(int, bool) error { return nil }}, false)
	if times, _ := service.started(""); len(times) != 1 {
		t.Fatalf("grabbed: %d counted, want still 1", len(times))
	}
}

// An unmonitor that failed after an upgrade search is tried again at the
// next run, until it works.
func TestRetryUnmonitors(t *testing.T) {
	failing := true
	var puts []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/movie/42" && r.Method == http.MethodGet:
			w.Write([]byte(`{"id":42,"monitored":true}`))
		case r.URL.Path == "/api/v3/movie/42" && r.Method == http.MethodPut:
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			puts = append(puts, body["monitored"] == true)
			if failing {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr", URL: srv.URL, APIKey: "k"}}}
	service := newTestService(t, cfg, *arrfake.Day("2026-10-25"))
	if err := service.noteLeftMonitored(leftMonitored{Kind: "movie", ServiceID: "r", ArrID: 42, Title: "Film"}); err != nil {
		t.Fatal(err)
	}
	service.retryUnmonitors(context.Background(), true)
	if len(puts) != 0 {
		t.Fatal("dry run unmonitored")
	}
	service.retryUnmonitors(context.Background(), false)
	if list, _ := service.leftMonitoredList(); len(list) != 1 {
		t.Fatalf("failed again, forgotten: %v", list)
	}
	failing = false
	service.retryUnmonitors(context.Background(), false)
	if list, _ := service.leftMonitoredList(); len(list) != 0 || len(puts) != 2 || puts[1] {
		t.Fatalf("left %v, asked %v", list, puts)
	}
}

// A search that errors out after it grabbed still counts its download.
func TestSearchErrorAfterAGrabCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/movie/lookup/tmdb":
			w.Write([]byte(`{"title":"Film","tmdbId":5,"digitalRelease":"2026-01-01T00:00:00Z"}`))
		case r.URL.Path == "/api/v3/rootfolder":
			w.Write([]byte(`[{"path":"/data/Films"}]`))
		case r.URL.Path == "/api/v3/qualityprofile":
			w.Write([]byte(`[{"id":7}]`))
		case r.URL.Path == "/api/v3/tag":
			w.Write([]byte(`[{"id":3,"label":"overtide-added"}]`))
		case r.URL.Path == "/api/v3/movie/42" && r.Method == http.MethodGet:
			w.Write([]byte(`{"id":42,"tags":[3],"monitored":false}`))
		case r.URL.Path == "/api/v3/movie/42":
			w.Write([]byte(`{}`))
		case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
			w.Write([]byte(`{"id":1}`))
		case r.URL.Path == "/api/v3/command/1":
			w.WriteHeader(http.StatusBadGateway) // following the search fails
		case r.URL.Path == "/api/v3/queue/details":
			w.Write([]byte(`[{}]`))
		case r.URL.Path == "/api/v3/history/movie":
			w.Write([]byte(`[{"eventType":"grabbed","date":"2026-10-25T12:00:00Z"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr", URL: srv.URL, APIKey: "k"}}}
	service := newTestService(t, cfg, *arrfake.Day("2026-10-25"))
	service.disk = func(config.Config, string, bool) string { return "" }
	tries.PollEvery = time.Millisecond
	entry := Entry{Key: movieKey(5), Kind: "movie", ServiceID: "r", ExternalID: 5, Source: FromOverfin, State: Waiting, ArrID: 42}
	service.try(context.Background(), cfg, &entry, false)
	if entry.State != Downloading {
		t.Fatalf("grabbed, then the search errored: %+v", entry)
	}
	if times, _ := service.started(""); len(times) != 1 {
		t.Fatalf("counted %d, want 1", len(times))
	}
}
