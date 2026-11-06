package adding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
)

// With "Manage all", Radarr's enabled lists stop adding and searching and
// their films go to Overfin, each list in its order; Sonarr's lists that
// add series add them unmonitored, untried, tagged, and its tagged series
// go to Overfin, the latest added first. A list switched off stays off and
// sends nothing; in dry run, nothing changes but the lists still go.
func TestSendLists(t *testing.T) {
	now := *arrfake.Day("2026-11-06")
	var sent [][]overfin.List
	overfinSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Lists []overfin.List }
		if r.Method != http.MethodPut || r.URL.Path != "/apps/v1/lists" || json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sent = append(sent, body.Lists)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer overfinSrv.Close()

	setup := func() (*arrfake.Arrs, config.Config) {
		fake := arrfake.New(now)
		fake.Lists["radarr"][1] = map[string]any{"id": 1, "name": "Latest", "enabled": true, "enableAuto": true, "searchOnAdd": true, "monitor": "movieOnly"}
		fake.Lists["radarr"][2] = map[string]any{"id": 2, "name": "Off", "enabled": false, "enableAuto": true, "searchOnAdd": true}
		fake.Lists["sonarr"][3] = map[string]any{"id": 3, "name": "Shows", "enableAutomaticAdd": true, "shouldMonitor": "all", "monitorNewItems": "all", "searchForMissingEpisodes": true, "tags": []any{}}
		fake.Lists["sonarr"][4] = map[string]any{"id": 4, "name": "Quiet", "enableAutomaticAdd": false, "shouldMonitor": "all", "tags": []any{}}
		fake.ListMovies = []map[string]any{
			{"tmdbId": 550, "title": "Fight Club", "lists": []int{1}},
			{"tmdbId": 0, "title": "No ID", "lists": []int{1}},
			{"tmdbId": 13, "title": "Forrest Gump", "lists": []int{1}},
		}
		listTag := fake.TagID("sonarr", model.TagList)
		fake.AddSeries(10, []int{listTag})
		fake.Series[10]["tmdbId"] = 1399
		fake.AddSeries(11, []int{listTag})
		fake.Series[11]["tmdbId"], fake.Series[11]["added"] = 1668, arrfake.ISO(now)
		fake.AddSeries(12, nil) // not from a list
		fake.Series[12]["tmdbId"] = 60059
		cfg := arrfake.Config(t, fake, config.ManageAll)
		cfg.Services[0].Name, cfg.Services[1].Name = "Radarr", "Sonarr"
		cfg.Overfin = config.Connection{URL: overfinSrv.URL, APIKey: "ofapp_k"}
		return fake, cfg
	}
	want := []overfin.List{
		{Name: "Radarr: Latest", Titles: []overfin.ListTitle{{Kind: "movie", TMDB: "550"}, {Kind: "movie", TMDB: "13"}}},
		{Name: "Sonarr: import lists", Titles: []overfin.ListTitle{{Kind: "series", TMDB: "1668"}, {Kind: "series", TMDB: "1399"}}},
	}

	fake, cfg := setup()
	service := newTestService(t, cfg, now)
	if err := service.sendLists(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if changes := fake.Mutations(); len(changes) != 0 {
		t.Errorf("dry run changed: %v", changes)
	}
	if err := service.sendLists(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || !reflect.DeepEqual(sent[0], want) || !reflect.DeepEqual(sent[1], want) {
		t.Errorf("sent %+v, want %+v twice", sent, want)
	}
	if l := fake.Lists["radarr"][1]; l["enableAuto"] != false || l["searchOnAdd"] != false {
		t.Errorf("Radarr's list: %v", l)
	}
	if l := fake.Lists["radarr"][2]; l["enableAuto"] != true {
		t.Errorf("the list switched off was changed: %v", l)
	}
	shows := fake.Lists["sonarr"][3]
	if shows["shouldMonitor"] != "none" || shows["monitorNewItems"] != "none" || shows["searchForMissingEpisodes"] != false ||
		!slices.Equal(fake.Labels("sonarr", shows["tags"]), []string{model.TagList}) {
		t.Errorf("Sonarr's list: %v", shows)
	}
	if l := fake.Lists["sonarr"][4]; l["shouldMonitor"] != "all" {
		t.Errorf("the list that adds nothing was changed: %v", l)
	}

	// Once taken, the lists aren't saved again.
	before := len(fake.Mutations())
	if err := service.sendLists(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	if changes := fake.Mutations()[before:]; len(changes) != 0 {
		t.Errorf("saved again: %v", changes)
	}

	// Without "Manage all", the lists are the operator's.
	fake, cfg = setup()
	cfg.Adding.Manage = config.ManageOwn
	sent = nil
	if err := newTestService(t, cfg, now).sendLists(context.Background(), cfg, false); err != nil || len(sent) != 0 || len(fake.Mutations()) != 0 {
		t.Errorf("managing its own: sent %v, changed %v, %v", sent, fake.Mutations(), err)
	}
}
