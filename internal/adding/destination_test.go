package adding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/tries"
)

// A suggestion goes where its category's destination says: an anime film
// to the second Radarr, its folder and profile; another film to the
// default destination, whose Radarr has several folders. While each
// search goes, Live names its title and step.
func TestDestinations(t *testing.T) {
	var mu sync.Mutex
	added := map[string]map[string]any{} // Radarr → the film it was given
	var service *Service
	var during []Live // what Live said while each search went
	radarr := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v3/movie/lookup/tmdb":
				w.Write([]byte(`{"title":"Film","tmdbId":` + r.URL.Query().Get("tmdbId") + `,"digitalRelease":"2026-01-01T00:00:00Z"}`))
			case r.URL.Path == "/api/v3/rootfolder":
				w.Write([]byte(`[{"path":"/a"},{"path":"/b"}]`)) // several: only a destination says which
			case r.URL.Path == "/api/v3/qualityprofile":
				w.Write([]byte(`[{"id":1},{"id":2}]`))
			case r.URL.Path == "/api/v3/tag" && r.Method == http.MethodGet:
				w.Write([]byte(`[{"id":3,"label":"overswell-added"}]`))
			case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodPost:
				var film map[string]any
				body, _ := io.ReadAll(r.Body)
				json.Unmarshal(body, &film)
				mu.Lock()
				added[name] = film
				mu.Unlock()
				w.Write([]byte(`{"id":42}`))
			case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodGet:
				w.Write([]byte(`[]`))
			case strings.HasPrefix(r.URL.Path, "/api/v3/movie/") && r.Method == http.MethodGet:
				w.Write([]byte(`{"id":42,"tags":[3],"monitored":false}`))
			case r.URL.Path == "/api/v3/command" && r.Method == http.MethodPost:
				live := service.Live()
				mu.Lock()
				during = append(during, live)
				mu.Unlock()
				w.Write([]byte(`{"id":1}`))
			case r.URL.Path == "/api/v3/command/1":
				w.Write([]byte(`{"status":"completed"}`))
			case r.URL.Path == "/api/v3/queue/details", r.URL.Path == "/api/v3/history/movie":
				w.Write([]byte(`[]`))
			default:
				w.Write([]byte(`{}`))
			}
		}))
	}
	films, anime := radarr("films"), radarr("anime")
	defer films.Close()
	defer anime.Close()
	overfin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apps/v1/history" {
			w.Write([]byte(`{"events":[]}`))
			return
		}
		w.Write([]byte(`{"titles":[{"kind":"movie","name":"Anime","tmdbId":"1","categories":["c-films","c-anime"]},{"kind":"movie","name":"Drama","tmdbId":"2","categories":["c-films"]}]}`))
	}))
	defer overfin.Close()
	tries.PollEvery = 0

	cfg := config.Config{Services: []config.Service{
		{ID: "r", Type: "radarr", URL: films.URL, APIKey: "k"},
		{ID: "r2", Type: "radarr", URL: anime.URL, APIKey: "k"},
	}}
	cfg.Adding.Mode = config.AddingOn
	cfg.Adding.Destinations = []config.Destination{
		{ServiceID: "r", RootFolder: "/b", QualityProfileID: 2},
		{Category: "c-anime", ServiceID: "r2", RootFolder: "/a", QualityProfileID: 1},
	}
	cfg.Overfin = config.Connection{URL: overfin.URL, APIKey: "ofapp_k"}
	service = newTestService(t, cfg, *arrfake.Day("2026-10-25"))
	service.disk = func(config.Config, string, bool) string { return "" }
	if err := service.ReadOverfin(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	// The live state named each search's title and step, then the run's end.
	if len(during) != 2 {
		t.Fatalf("live during the searches: %+v", during)
	}
	for _, live := range during {
		if !live.Running || live.Phase != "Trying titles" || live.Title == "" || live.Step != "Radarr is searching its trackers" {
			t.Errorf("live during a search: %+v", live)
		}
	}
	if live := service.Live(); live.Running || live.Searches != 2 || live.Budget != config.DefaultSearches {
		t.Errorf("live after the run: %+v", live)
	}
	for name, want := range map[string]struct {
		tmdb    float64
		folder  string
		profile float64
	}{"anime": {1, "/a", 1}, "films": {2, "/b", 2}} {
		film := added[name]
		if film == nil || film["tmdbId"] != want.tmdb || film["rootFolderPath"] != want.folder || film["qualityProfileId"] != want.profile {
			t.Errorf("%s got %v, want TMDB %v in %s with profile %v", name, film, want.tmdb, want.folder, want.profile)
		}
	}
}
