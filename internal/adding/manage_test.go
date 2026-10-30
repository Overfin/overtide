package adding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// fakeArrs is a Radarr and a Sonarr in one server (/radarr, /sonarr),
// keeping their tags, films, series and episodes, and the changes asked.
type fakeArrs struct {
	mu      sync.Mutex
	now     time.Time
	tags    map[string]map[int]string // per app
	films   map[int]map[string]any
	series  map[int]map[string]any
	eps     map[int]map[string]any
	queued  map[int]bool
	grabOn  map[int]bool // an episode search for these starts a download
	changes []string
}

func newFakeArrs(now time.Time) *fakeArrs {
	return &fakeArrs{now: now, tags: map[string]map[int]string{"radarr": {}, "sonarr": {}},
		films: map[int]map[string]any{}, series: map[int]map[string]any{}, eps: map[int]map[string]any{},
		queued: map[int]bool{}, grabOn: map[int]bool{}}
}

func (f *fakeArrs) tagID(app, label string) int {
	for id, l := range f.tags[app] {
		if l == label {
			return id
		}
	}
	id := len(f.tags[app]) + 1
	f.tags[app][id] = label
	return id
}

func (f *fakeArrs) labels(app string, raw any) []string {
	var out []string
	for _, id := range ints(raw) {
		out = append(out, f.tags[app][id])
	}
	slices.Sort(out)
	return out
}

func ints(raw any) []int {
	var out []int
	switch v := raw.(type) {
	case []int:
		out = append(out, v...)
	case []any:
		for _, x := range v {
			out = append(out, int(x.(float64)))
		}
	}
	return out
}

func (f *fakeArrs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	app, path, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	path = "/" + path
	var body map[string]any
	if r.Method != http.MethodGet {
		json.NewDecoder(r.Body).Decode(&body)
		f.changes = append(f.changes, fmt.Sprintf("%s %s %s", app, r.Method, path))
	}
	write := func(v any) { json.NewEncoder(w).Encode(v) }
	list := func(m map[int]map[string]any) []map[string]any {
		var out []map[string]any
		for _, id := range slices.Sorted(func(yield func(int) bool) {
			for id := range m {
				if !yield(id) {
					return
				}
			}
		}) {
			out = append(out, m[id])
		}
		return out
	}
	editTags := func(m map[int]map[string]any, idsKey string) {
		for _, id := range ints(body[idsKey]) {
			item := m[id]
			if body["tags"] != nil {
				tags := ints(item["tags"])
				for _, tag := range ints(body["tags"]) {
					if body["applyTags"] == "add" && !slices.Contains(tags, tag) {
						tags = append(tags, tag)
					}
					if body["applyTags"] == "remove" {
						tags = slices.DeleteFunc(tags, func(t int) bool { return t == tag })
					}
				}
				item["tags"] = tags
			}
			if monitored, ok := body["monitored"].(bool); ok {
				item["monitored"] = monitored
			}
		}
	}
	switch {
	case path == "/api/v3/tag" && r.Method == http.MethodGet:
		var out []map[string]any
		for id, label := range f.tags[app] {
			out = append(out, map[string]any{"id": id, "label": label})
		}
		write(out)
	case path == "/api/v3/tag":
		write(map[string]any{"id": f.tagID(app, body["label"].(string))})
	case path == "/api/v3/movie" && r.Method == http.MethodGet:
		write(list(f.films))
	case path == "/api/v3/movie/editor":
		editTags(f.films, "movieIds")
	case path == "/api/v3/series" && r.Method == http.MethodGet:
		write(list(f.series))
	case path == "/api/v3/series/editor":
		editTags(f.series, "seriesIds")
	case strings.HasPrefix(path, "/api/v3/series/") && r.Method == http.MethodGet:
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/series/"))
		write(f.series[id])
	case strings.HasPrefix(path, "/api/v3/series/") && r.Method == http.MethodPut:
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/series/"))
		f.series[id] = body
		// Sonarr carries a season's monitoring to its episodes.
		for _, raw := range body["seasons"].([]any) {
			season := raw.(map[string]any)
			for _, e := range f.eps {
				if e["seriesId"] == id && e["seasonNumber"] == int(season["seasonNumber"].(float64)) {
					e["monitored"] = season["monitored"]
				}
			}
		}
	case path == "/api/v3/wanted/missing":
		monitored := r.URL.Query().Get("monitored") == "true"
		var out []map[string]any
		for _, e := range list(f.eps) {
			aired, _ := time.Parse(time.RFC3339, e["airDateUtc"].(string))
			if e["hasFile"] == false && e["monitored"] == monitored && aired.Before(f.now) {
				out = append(out, e)
			}
		}
		write(map[string]any{"totalRecords": len(out), "records": out})
	case path == "/api/v3/calendar":
		start, _ := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
		end, _ := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
		var out []map[string]any
		for _, e := range list(f.eps) {
			aired, _ := time.Parse(time.RFC3339, e["airDateUtc"].(string))
			if !aired.Before(start) && !aired.After(end) {
				out = append(out, e)
			}
		}
		write(out)
	case path == "/api/v3/queue/details":
		var out []map[string]any
		for id := range f.queued {
			out = append(out, map[string]any{"episodeId": id})
		}
		write(out)
	case path == "/api/v3/episode/monitor":
		for _, id := range ints(body["episodeIds"]) {
			f.eps[id]["monitored"] = body["monitored"]
		}
	case strings.HasPrefix(path, "/api/v3/episode/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/episode/"))
		if f.eps[id] == nil {
			http.NotFound(w, r)
			return
		}
		write(f.eps[id])
	case path == "/api/v3/command" && r.Method == http.MethodPost:
		for _, id := range ints(body["episodeIds"]) {
			f.changes = append(f.changes, fmt.Sprintf("search episode %d", id))
			if f.grabOn[id] {
				f.queued[id] = true
			}
		}
		write(map[string]any{"id": 1})
	case strings.HasPrefix(path, "/api/v3/command/"):
		write(map[string]any{"status": "completed"})
	case path == "/api/v3/rootfolder":
		write([]map[string]any{{"path": "/data/Series"}})
	case path == "/api/v3/qualityprofile":
		write([]map[string]any{{"id": 1}})
	default:
		write(map[string]any{})
	}
}

func (f *fakeArrs) mutations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.changes)
}

func manageTestService(t *testing.T, fake *fakeArrs, manage string) (*Service, config.Config) {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	cfg := config.Config{Services: []config.Service{
		{ID: "r", Type: "radarr", URL: srv.URL + "/radarr", APIKey: "k"},
		{ID: "s", Type: "sonarr", URL: srv.URL + "/sonarr", APIKey: "k"},
	}}
	cfg.Adding.Mode, cfg.Adding.Manage = config.AddingOn, manage
	service := newTestService(t, cfg, fake.now)
	service.disk = func(config.Config, string, bool) string { return "" }
	pollEvery = time.Millisecond
	return service, cfg
}

func iso(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// The legacy tag goes, once: the titles Overtide added get the tag of where
// they came from, the others overtide-found.
func TestRetagReplacesTheLegacyTag(t *testing.T) {
	now := *day("2026-10-30")
	fake := newFakeArrs(now)
	legacy := fake.tagID("radarr", model.LegacyTag)
	for id := 1; id <= 4; id++ {
		fake.films[id] = map[string]any{"id": id, "tmdbId": id, "tags": []int{legacy}, "monitored": true, "hasFile": true}
	}
	fake.films[4]["tags"] = []int{} // never managed
	service, cfg := manageTestService(t, fake, config.ManageOwn)
	entries := []Entry{
		{Key: movieKey(1), Kind: model.Movie, ServiceID: "r", Source: FromOverfin, ArrID: 1},
		{Key: movieKey(2), Kind: model.Movie, ServiceID: "r", Source: FromRequest, ArrID: 2},
	}
	if err := service.retag(context.Background(), cfg, entries); err != nil {
		t.Fatal(err)
	}
	want := map[int][]string{1: {model.TagAdded}, 2: {model.TagRequested}, 3: {model.TagFound}, 4: nil}
	for id, labels := range want {
		if got := fake.labels("radarr", fake.films[id]["tags"]); !slices.Equal(got, labels) {
			t.Errorf("film %d: %v, want %v", id, got, labels)
		}
	}
	before := len(fake.mutations())
	if err := service.retag(context.Background(), cfg, entries); err != nil {
		t.Fatal(err)
	}
	if after := len(fake.mutations()); after != before {
		t.Errorf("retagged again: %v", fake.mutations()[before:])
	}
}

// "Manage all" takes every title, and keeps what Radarr and Sonarr wanted:
// a monitored film without its file goes in line, a removed one (not
// monitored) stays unwanted; a followed series' recent episodes are
// monitored for the RSS, older missing ones wanted; an unfollowed series
// gets nothing new. Dry run changes nothing.
func TestTakeAll(t *testing.T) {
	now := *day("2026-10-30")
	build := func() *fakeArrs {
		fake := newFakeArrs(now)
		fake.films[1] = map[string]any{"id": 1, "tmdbId": 11, "title": "Has its file", "monitored": true, "hasFile": true, "added": iso(now.AddDate(-1, 0, 0))}
		fake.films[2] = map[string]any{"id": 2, "tmdbId": 12, "title": "Wanted", "monitored": true, "hasFile": false, "added": iso(now.AddDate(-1, 0, 0))}
		fake.films[3] = map[string]any{"id": 3, "tmdbId": 13, "title": "Removed", "monitored": false, "hasFile": false, "added": iso(now.AddDate(-1, 0, 0))}
		season := func(n int, monitored bool) map[string]any {
			return map[string]any{"seasonNumber": n, "monitored": monitored}
		}
		fake.series[1] = map[string]any{"id": 1, "title": "Airing", "monitored": true, "monitorNewItems": "all", "seasons": []any{season(1, true), season(2, true)}, "added": iso(now.AddDate(-1, 0, 0))}
		fake.series[2] = map[string]any{"id": 2, "title": "Dropped", "monitored": true, "monitorNewItems": "none", "seasons": []any{season(1, false)}, "added": iso(now.AddDate(-1, 0, 0))}
		ep := func(id, series, season int, aired time.Time, file, monitored bool) {
			fake.eps[id] = map[string]any{"id": id, "seriesId": series, "seasonNumber": season, "episodeNumber": id, "airDateUtc": iso(aired), "hasFile": file, "monitored": monitored}
		}
		ep(101, 1, 1, now.AddDate(0, 0, -60), false, true)  // old, missing, wanted
		ep(102, 1, 2, now.AddDate(0, 0, -3), false, true)   // recent, missing: the window's
		ep(103, 1, 2, now.Add(24*time.Hour), false, true)   // tomorrow: the window's
		ep(104, 1, 2, now.AddDate(0, 0, -1), true, true)    // has its file
		ep(105, 1, 2, now.AddDate(0, 0, 30), false, true)   // later: not yet
		ep(201, 2, 1, now.AddDate(0, 0, -2), false, false)  // unfollowed series
		ep(106, 1, 1, now.AddDate(0, 0, -40), false, false) // unmonitored: unwanted
		return fake
	}

	fake := build()
	service, cfg := manageTestService(t, fake, config.ManageAll)
	entries, err := service.takeAll(context.Background(), cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if changes := fake.mutations(); len(changes) != 0 {
		t.Errorf("dry run changed: %v", changes)
	}
	summary, _ := service.Summary()
	if !summary.DryRun || summary.Tagged != 5 || summary.Unmonitored != 2 || summary.SeriesTaken != 1 || summary.FilmsQueued != 1 || summary.EpisodesWanted != 1 || summary.Following != 1 || summary.Monitored != 2 {
		t.Errorf("dry-run summary: %+v", summary)
	}
	if len(entries) != 1 {
		t.Errorf("dry run entries: %+v", entries)
	}

	fake = build()
	service, cfg = manageTestService(t, fake, config.ManageAll)
	entries, err = service.takeAll(context.Background(), cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for id, film := range fake.films {
		if film["monitored"] != false || !slices.Equal(fake.labels("radarr", film["tags"]), []string{model.TagFound}) {
			t.Errorf("film %d: %v", id, film)
		}
	}
	if len(entries) != 1 || entries[0].ArrID != 2 || entries[0].Source != FromLibrary || entries[0].State != Waiting {
		t.Errorf("in line: %+v", entries)
	}
	for id, series := range fake.series {
		if series["monitorNewItems"] != "none" || series["monitored"] != true || !slices.Equal(fake.labels("sonarr", series["tags"]), []string{model.TagFound}) {
			t.Errorf("series %d: %v", id, series)
		}
	}
	monitored := map[int]bool{}
	for id, e := range fake.eps {
		monitored[id] = e["monitored"] == true
	}
	if want := map[int]bool{101: false, 102: true, 103: true, 104: false, 105: false, 201: false, 106: false}; !mapsEqual(monitored, want) {
		t.Errorf("episodes monitored: %v, want %v", monitored, want)
	}
	states, _ := service.episodeStates()
	if wanted := states["s"].Wanted; len(wanted) != 1 || wanted[101].SeriesID != 1 {
		t.Errorf("wanted: %+v", wanted)
	}

	// The next pass changes nothing more, and a film added since is new.
	before := len(fake.mutations())
	fake.films[4] = map[string]any{"id": 4, "tmdbId": 14, "title": "Added since", "monitored": true, "hasFile": false, "added": iso(now.Add(time.Hour))}
	service.now = func() time.Time { return now.Add(2 * time.Hour) }
	if entries, err = service.takeAll(context.Background(), cfg, entries, false); err != nil {
		t.Fatal(err)
	}
	if got := fake.labels("radarr", fake.films[4]["tags"]); !slices.Equal(got, []string{model.TagNew}) || fake.films[4]["monitored"] != false || len(entries) != 2 {
		t.Errorf("film added since: %v, %d in line", fake.films[4], len(entries))
	}
	for _, change := range fake.mutations()[before:] {
		if strings.Contains(change, "sonarr PUT /api/v3/series/") {
			t.Errorf("series taken again: %s", change)
		}
	}
}

func mapsEqual(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Wanted episodes are searched one at a time within the run's budget: a
// grab is followed until imported, then leaves the list; nothing found is
// tried again later.
func TestSearchEpisodes(t *testing.T) {
	now := *day("2026-10-30")
	fake := newFakeArrs(now)
	for _, id := range []int{1, 2, 3} {
		fake.eps[id] = map[string]any{"id": id, "seriesId": 9, "seasonNumber": 1, "episodeNumber": id, "airDateUtc": iso(now.AddDate(0, -1, 0)), "hasFile": false, "monitored": false}
	}
	fake.grabOn[1] = true
	service, cfg := manageTestService(t, fake, config.ManageAll)
	states := map[string]*sonarrState{"s": {Follow: map[int]bool{}, Wanted: map[int]episodeWant{1: {SeriesID: 9}, 2: {SeriesID: 9}, 3: {SeriesID: 9, NextTry: now.Add(time.Hour)}}}}
	if err := service.saveEpisodeStates(states); err != nil {
		t.Fatal(err)
	}
	if err := service.searchEpisodes(context.Background(), cfg, 2, false); err != nil {
		t.Fatal(err)
	}
	var searched []string
	for _, change := range fake.mutations() {
		if strings.HasPrefix(change, "search") {
			searched = append(searched, change)
		}
	}
	slices.Sort(searched)
	if !slices.Equal(searched, []string{"search episode 1", "search episode 2"}) {
		t.Errorf("searched: %v", searched)
	}
	states, _ = service.episodeStates()
	wanted := states["s"].Wanted
	if !wanted[1].Grabbed || wanted[2].Grabbed || wanted[2].Tries != 1 || !wanted[2].NextTry.After(now) {
		t.Errorf("after the searches: %+v", wanted)
	}
	// Imported: it leaves the list.
	fake.mu.Lock()
	delete(fake.queued, 1)
	fake.eps[1]["hasFile"] = true
	fake.mu.Unlock()
	if err := service.searchEpisodes(context.Background(), cfg, 0, false); err != nil {
		t.Fatal(err)
	}
	states, _ = service.episodeStates()
	if _, there := states["s"].Wanted[1]; there {
		t.Errorf("imported episode still wanted: %+v", states["s"].Wanted)
	}
}
