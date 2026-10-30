package adding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/overfin/overtide/internal/config"
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
	service := newTestService(t, cfg, *day("2026-10-26"))
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
	release.State, release.MonitoredAt, release.ReleaseAt = NotReleased, *day("2026-10-24"), *day("2026-10-25")
	film = &fakeFilm{queued: true}
	got = followOne(t, film, release, false)
	if got.State != Downloading || !got.Unmonitored || len(film.monitored) != 1 || film.monitored[0] {
		t.Errorf("grabbed on release: %+v, asked %v", got, film.monitored)
	}
}
