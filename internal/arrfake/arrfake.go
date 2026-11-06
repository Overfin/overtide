// Package arrfake is a Radarr and a Sonarr for tests, shared by the adding
// side's and the Sonarr driver's.
package arrfake

import (
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
)

// Arrs is a Radarr and a Sonarr in one server (/radarr, /sonarr),
// keeping their tags, films, series and episodes, and the changes asked.
type Arrs struct {
	Mu      sync.Mutex
	Now     time.Time
	Tags    map[string]map[int]string // per app
	Films   map[int]map[string]any
	Series  map[int]map[string]any
	Eps     map[int]map[string]any
	Queued  map[int]bool
	GrabOn  map[int]bool // an episode search for these starts a download
	Changes []string
	// Whole seasons: releases per season, the queue's downloads, the
	// files manual import sees, episode files, and the imports asked.
	Releases map[int][]map[string]any
	Queue    []map[string]any
	ImportAt []map[string]any
	Files    map[int]map[string]any
	Imported [][]string
	// Below their cutoff, per app, and series' history by series ID.
	FullQueue []map[string]any
	Cutoff    map[string][]map[string]any
	History   map[string][]map[string]any
	// With auto-import off: the setting, the grabs, each download's events
	// and files. A download without files listed errors, as Sonarr does for
	// one still going.
	AutoImportOff bool
	ImportedTo    map[string][]int
	Grabs         []map[string]any
	Events        map[string][]map[string]any
	Downloads     map[string][]map[string]any
	// Connect entries per app, by ID; a test sends the entry's webhook a
	// test event, as Radarr and Sonarr do.
	Notifications map[string]map[int]map[string]any
}

// New is an empty Radarr and Sonarr, at now.
func New(now time.Time) *Arrs {
	return &Arrs{ImportedTo: map[string][]int{}, Now: now, Tags: map[string]map[int]string{"radarr": {}, "sonarr": {}},
		Films: map[int]map[string]any{}, Series: map[int]map[string]any{}, Eps: map[int]map[string]any{},
		Queued: map[int]bool{}, GrabOn: map[int]bool{}, Releases: map[int][]map[string]any{}, Files: map[int]map[string]any{},
		Cutoff: map[string][]map[string]any{}, History: map[string][]map[string]any{},
		Notifications: map[string]map[int]map[string]any{"radarr": {}, "sonarr": {}}}
}

// TagID is the ID of the app's tag, made if need be.
func (f *Arrs) TagID(app, label string) int {
	for id, l := range f.Tags[app] {
		if l == label {
			return id
		}
	}
	id := len(f.Tags[app]) + 1
	f.Tags[app][id] = label
	return id
}

// Labels are the app's tags of those IDs, sorted.
func (f *Arrs) Labels(app string, raw any) []string {
	var out []string
	for _, id := range ints(raw) {
		out = append(out, f.Tags[app][id])
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

func (f *Arrs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	app, path, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	path = "/" + path
	var body map[string]any
	if r.Method != http.MethodGet {
		json.NewDecoder(r.Body).Decode(&body)
		f.Changes = append(f.Changes, fmt.Sprintf("%s %s %s", app, r.Method, path))
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
		for id, label := range f.Tags[app] {
			out = append(out, map[string]any{"id": id, "label": label})
		}
		write(out)
	case path == "/api/v3/tag":
		write(map[string]any{"id": f.TagID(app, body["label"].(string))})
	case path == "/api/v3/movie" && r.Method == http.MethodGet:
		write(list(f.Films))
	case path == "/api/v3/movie/editor":
		editTags(f.Films, "movieIds")
	case path == "/api/v3/series" && r.Method == http.MethodGet:
		write(list(f.Series))
	case path == "/api/v3/series/editor":
		editTags(f.Series, "seriesIds")
	case strings.HasPrefix(path, "/api/v3/series/") && r.Method == http.MethodGet:
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/series/"))
		write(f.Series[id])
	case strings.HasPrefix(path, "/api/v3/series/") && r.Method == http.MethodPut:
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/series/"))
		f.Series[id] = body
		// Sonarr carries a season's monitoring to its episodes.
		for _, raw := range body["seasons"].([]any) {
			season := raw.(map[string]any)
			for _, e := range f.Eps {
				if e["seriesId"] == id && e["seasonNumber"] == int(season["seasonNumber"].(float64)) {
					e["monitored"] = season["monitored"]
				}
			}
		}
	case path == "/api/v3/wanted/cutoff":
		out := f.Cutoff[app]
		write(map[string]any{"totalRecords": len(out), "records": out})
	case path == "/api/v3/history/series":
		out := f.History[r.URL.Query().Get("seriesId")]
		if out == nil {
			out = []map[string]any{}
		}
		write(out)
	case path == "/api/v3/history/movie":
		write([]map[string]any{})
	case path == "/api/v3/movie/lookup/tmdb":
		write(map[string]any{"status": "released"})
	case strings.HasPrefix(path, "/api/v3/movie/") && r.Method == http.MethodGet:
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/movie/"))
		write(f.Films[id])
	case strings.HasPrefix(path, "/api/v3/movie/") && r.Method == http.MethodPut:
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/movie/"))
		f.Films[id] = body
		f.Changes = append(f.Changes, fmt.Sprintf("film %d monitored %v", id, body["monitored"]))
	case path == "/api/v3/wanted/missing":
		monitored := r.URL.Query().Get("monitored") == "true"
		var out []map[string]any
		for _, e := range list(f.Eps) {
			aired, _ := time.Parse(time.RFC3339, e["airDateUtc"].(string))
			if e["hasFile"] == false && e["monitored"] == monitored && aired.Before(f.Now) {
				out = append(out, e)
			}
		}
		write(map[string]any{"totalRecords": len(out), "records": out})
	case path == "/api/v3/calendar":
		start, _ := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
		end, _ := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
		var out []map[string]any
		for _, e := range list(f.Eps) {
			aired, _ := time.Parse(time.RFC3339, e["airDateUtc"].(string))
			if !aired.Before(start) && !aired.After(end) {
				out = append(out, e)
			}
		}
		write(out)
	case path == "/api/v3/queue/details" && (r.URL.Query().Get("seriesId") != "" || r.URL.Query().Get("movieId") != ""):
		write(f.Queue)
	case path == "/api/v3/queue/bulk":
		f.Changes = append(f.Changes, fmt.Sprintf("forget %v removeFromClient=%s", ints(body["ids"]), r.URL.Query().Get("removeFromClient")))
	case path == "/api/v3/queue/details" && f.FullQueue != nil:
		if app == "sonarr" {
			write(f.FullQueue)
		} else {
			write([]map[string]any{})
		}
	case path == "/api/v3/release" && r.Method == http.MethodGet:
		season, _ := strconv.Atoi(r.URL.Query().Get("seasonNumber"))
		write(f.Releases[season])
	case path == "/api/v3/release":
		f.Changes = append(f.Changes, fmt.Sprintf("grab %v", body["guid"]))
	case path == "/api/v3/config/downloadclient":
		write(map[string]any{"enableCompletedDownloadHandling": !f.AutoImportOff})
	case path == "/api/v3/history/since":
		write(f.Grabs)
	case path == "/api/v3/history":
		write(map[string]any{"records": f.Events[r.URL.Query().Get("downloadId")]})
	case path == "/api/v3/manualimport" && f.AutoImportOff:
		files, ok := f.Downloads[r.URL.Query().Get("downloadId")]
		if !ok || r.URL.Query().Get("seriesId") != "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		write(files)
	case path == "/api/v3/manualimport":
		write(f.ImportAt)
	case path == "/api/v3/episodefile":
		write(list(f.Files))
	case path == "/api/v3/queue/details":
		var out []map[string]any
		for id := range f.Queued {
			out = append(out, map[string]any{"episodeId": id})
		}
		write(out)
	case path == "/api/v3/episode":
		series, _ := strconv.Atoi(r.URL.Query().Get("seriesId"))
		var out []map[string]any
		for _, e := range list(f.Eps) {
			if e["seriesId"] == series {
				out = append(out, e)
			}
		}
		write(out)
	case path == "/api/v3/episode/monitor":
		for _, id := range ints(body["episodeIds"]) {
			f.Eps[id]["monitored"] = body["monitored"]
		}
	case strings.HasPrefix(path, "/api/v3/episode/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/episode/"))
		if f.Eps[id] == nil {
			http.NotFound(w, r)
			return
		}
		write(f.Eps[id])
	case path == "/api/v3/command" && r.Method == http.MethodPost && body["name"] == "ManualImport":
		var paths []string
		for _, raw := range body["files"].([]any) {
			paths = append(paths, raw.(map[string]any)["path"].(string))
		}
		slices.Sort(paths)
		f.Imported = append(f.Imported, paths)
		for _, raw := range body["files"].([]any) {
			file := raw.(map[string]any)
			f.ImportedTo[file["path"].(string)] = ints(file["episodeIds"])
		}
		write(map[string]any{"id": 2})
	case path == "/api/v3/command" && r.Method == http.MethodPost:
		switch body["name"] {
		case "SeasonSearch":
			f.Changes = append(f.Changes, fmt.Sprintf("search season %v/%v", body["seriesId"], body["seasonNumber"]))
		case "MoviesSearch":
			f.Changes = append(f.Changes, fmt.Sprintf("search film %v", ints(body["movieIds"])))
		}
		for _, id := range ints(body["episodeIds"]) {
			f.Changes = append(f.Changes, fmt.Sprintf("search episode %d", id))
			if f.GrabOn[id] {
				f.Queued[id] = true
			}
		}
		write(map[string]any{"id": 1})
	case strings.HasPrefix(path, "/api/v3/command/"):
		write(map[string]any{"status": "completed"})
	case path == "/api/v3/rootfolder":
		write([]map[string]any{{"path": "/data/Series"}})
	case path == "/api/v3/qualityprofile":
		write([]map[string]any{{"id": 1}})
	case path == "/api/v3/notification/schema":
		write([]map[string]any{{"implementation": "Webhook", "name": "", "onGrab": false, "onMovieAdded": false, "onSeriesAdd": false,
			"fields": []any{map[string]any{"name": "url"}, map[string]any{"name": "method", "value": 1}, map[string]any{"name": "username"}, map[string]any{"name": "password"}}}})
	case path == "/api/v3/notification/test":
		field := func(name string) string {
			for _, f := range body["fields"].([]any) {
				if f.(map[string]any)["name"] == name {
					v, _ := f.(map[string]any)["value"].(string)
					return v
				}
			}
			return ""
		}
		request, _ := http.NewRequest(http.MethodPost, field("url"), strings.NewReader(`{"eventType":"Test"}`))
		if request == nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		request.SetBasicAuth(field("username"), field("password"))
		request.Header.Set("Content-Type", "application/json")
		answer, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
		if err != nil || answer.StatusCode/100 != 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answer.Body.Close()
		write(map[string]any{})
	case path == "/api/v3/notification" && r.Method == http.MethodGet:
		write(list(f.Notifications[app]))
	case path == "/api/v3/notification":
		id := len(f.Notifications[app]) + 1
		body["id"] = id
		f.Notifications[app][id] = body
		write(body)
	case strings.HasPrefix(path, "/api/v3/notification/") && r.Method == http.MethodPut:
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/api/v3/notification/"))
		f.Notifications[app][id] = body
		write(body)
	default:
		write(map[string]any{})
	}
}

// Mutations are the changes asked, in order.
func (f *Arrs) Mutations() []string {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return slices.Clone(f.Changes)
}

// Config is a Radarr ("r") and a Sonarr ("s") on the fake, with adding on
// and manage as "Manage all" says.
func Config(t testing.TB, fake *Arrs, manage string) config.Config {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	cfg := config.Config{Services: []config.Service{
		{ID: "r", Type: "radarr", URL: srv.URL + "/radarr", APIKey: "k"},
		{ID: "s", Type: "sonarr", URL: srv.URL + "/sonarr", APIKey: "k"},
	}}
	cfg.Adding.Mode, cfg.Adding.Manage = config.AddingOn, manage
	return cfg
}

// Season is a season as Sonarr's series list has it.
func Season(number, files, total int) map[string]any {
	return map[string]any{"seasonNumber": number, "monitored": false, "statistics": map[string]any{"episodeFileCount": files, "totalEpisodeCount": total}}
}

// AddSeries adds a series to Sonarr, added a year ago.
func (f *Arrs) AddSeries(id int, tags []int, seasons ...any) {
	f.Series[id] = map[string]any{"id": id, "title": "Series " + strconv.Itoa(id), "tvdbId": 1000 + id, "path": "/data/Series/" + strconv.Itoa(id), "monitored": true, "monitorNewItems": "none", "tags": tags, "seasons": seasons, "added": ISO(f.Now.AddDate(-1, 0, 0))}
}

// AddEpisode adds an episode to Sonarr, unmonitored.
func (f *Arrs) AddEpisode(id, series, seasonNumber int, aired time.Time, hasFile bool) {
	f.Eps[id] = map[string]any{"id": id, "seriesId": series, "seasonNumber": seasonNumber, "episodeNumber": id, "airDateUtc": ISO(aired), "hasFile": hasFile, "monitored": false}
}

// Searches are the searches and grabs asked, in order.
func (f *Arrs) Searches() []string {
	var out []string
	for _, change := range f.Mutations() {
		if strings.HasPrefix(change, "search") || strings.HasPrefix(change, "grab") {
			out = append(out, change)
		}
	}
	return out
}

// Day is midnight UTC of a 2006-01-02 date.
func Day(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

// ISO is a time as Radarr and Sonarr write it.
func ISO(t time.Time) string { return t.UTC().Format(time.RFC3339) }
