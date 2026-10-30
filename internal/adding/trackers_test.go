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

// With the trackers unavailable, a search that found nothing isn't a try:
// the film waits for a later run without a try counted, says why, and the
// run stops searching. With them there, it's a try as before.
func TestTryNotCountedWhenTrackersUnavailable(t *testing.T) {
	var mu sync.Mutex
	searches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
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
			w.Write([]byte(`{"id":42}`))
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

	for _, down := range []bool{true, false} {
		mu.Lock()
		searches = 0
		mu.Unlock()
		cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr", URL: srv.URL, APIKey: "k"}}}
		cfg.Adding.Mode = config.AddingOn
		now := *day("2026-10-25")
		service := newTestService(t, cfg, now)
		service.disk = func(config.Config, string, bool) string { return "" }
		service.Trackers = func(time.Time) (bool, string) {
			if down {
				return true, "TrackerA unavailable"
			}
			return false, ""
		}
		var entries []Entry
		for i := 1; i <= 3; i++ {
			entries = append(entries, Entry{Key: movieKey(i), Kind: "movie", ServiceID: "r", ExternalID: i, Source: FromOverfin, State: Waiting, Position: i, AddedAt: now})
		}
		if err := service.save(entries); err != nil {
			t.Fatal(err)
		}
		if err := service.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		saved, _ := service.load()
		mu.Lock()
		got := searches
		mu.Unlock()
		if down {
			if got != 1 || saved[0].Tries != 0 || !strings.Contains(saved[0].Result, "TrackerA unavailable") || !saved[0].NextTry.After(now) {
				t.Errorf("trackers down: %d searches, first %+v", got, saved[0])
			}
		} else if got != 3 || saved[0].Tries != 1 || !strings.Contains(saved[0].Result, "Nothing found") {
			t.Errorf("trackers there: %d searches, first %+v", got, saved[0])
		}
	}
}

// A wanted episode and a whole season aren't counted either.
func TestEpisodesAndSeasonsNotCountedWhenTrackersUnavailable(t *testing.T) {
	now := *day("2026-10-30")
	fake := newFakeArrs(now)
	fake.eps[1] = map[string]any{"id": 1, "seriesId": 5, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": iso(now.AddDate(0, 0, -30)), "hasFile": false, "episodeFileId": 0, "monitored": false}
	fake.eps[2] = map[string]any{"id": 2, "seriesId": 6, "seasonNumber": 0, "episodeNumber": 1, "airDateUtc": iso(now.AddDate(0, 0, -30)), "hasFile": false, "episodeFileId": 0, "monitored": false}
	service, cfg := manageTestService(t, fake, config.ManageAll)
	service.Trackers = func(time.Time) (bool, string) { return true, "no tracker answered" }
	states := map[string]*sonarrState{"s": {Wanted: map[int]episodeWant{1: {SeriesID: 5, Season: 1}, 2: {SeriesID: 6, Season: 0}}}}
	if err := service.saveEpisodeStates(states); err != nil {
		t.Fatal(err)
	}
	if _, err := service.replaceSeasons(context.Background(), cfg, 3, false); err != nil {
		t.Fatal(err)
	}
	service.trackersDown = ""
	if err := service.searchEpisodes(context.Background(), cfg, 3, false); err != nil {
		t.Fatal(err)
	}
	states, _ = service.episodeStates()
	pack := states["s"].Seasons[seasonKey(5, 1)]
	if pack == nil || pack.Tries != 0 || !pack.NextTry.After(now) {
		t.Errorf("season: %+v", pack)
	}
	if want := states["s"].Wanted[2]; want.Tries != 0 || !want.NextTry.After(now) {
		t.Errorf("episode: %+v", want)
	}
}
