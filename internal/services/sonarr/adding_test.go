package sonarr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overfin/overtide/internal/services/arr"
	"sync/atomic"
	"time"
)

// Adding sends the lookup back with the folder and profile, tagged, no
// season monitored and nothing searched; tagging a series that has the tag
// saves nothing; a season counts as there once it has a file; an unknown
// TheTVDB ID is "not found".
func TestAddingCalls(t *testing.T) {
	var added map[string]any
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/series/lookup" && r.URL.Query().Get("term") == "tvdb:1":
			_, _ = w.Write([]byte(`[{"title":"Show","year":2026,"tvdbId":1,"status":"continuing","seasons":[{"seasonNumber":1}]}]`))
		case r.URL.Path == "/api/v3/series/lookup":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/series" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&added)
			_, _ = w.Write([]byte(`{"id":42}`))
		case r.URL.Path == "/api/v3/series/42" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"id":42,"tags":[3],"seasons":[{"seasonNumber":1,"statistics":{"episodeFileCount":2}},{"seasonNumber":2,"statistics":{"episodeFileCount":0}}]}`))
		case r.URL.Path == "/api/v3/series/42" && r.Method == http.MethodPut:
			puts++
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer server.Close()
	client := New(server.URL, "key")

	lookup, err := client.LookupSeries(1)
	if err != nil || lookup.Title != "Show" {
		t.Fatalf("lookup: %#v %v", lookup, err)
	}
	if _, err := client.LookupSeries(2); !arr.IsNotFound(err) {
		t.Fatalf("unknown series: %v", err)
	}
	id, err := client.AddSeries(lookup, "/data/Series", 7, 3)
	if err != nil || id != 42 {
		t.Fatalf("add: %d %v", id, err)
	}
	options, _ := added["addOptions"].(map[string]any)
	if added["rootFolderPath"] != "/data/Series" || added["qualityProfileId"] != float64(7) || options["monitor"] != "none" || options["searchForMissingEpisodes"] != false || added["title"] != "Show" {
		t.Fatalf("added with %#v", added)
	}
	if err := client.TagSeries(42, 3); err != nil || puts != 0 {
		t.Fatalf("tagging a tagged series: %v, %d saves", err, puts)
	}
	if err := client.TagSeries(42, 4); err != nil || puts != 1 {
		t.Fatalf("tagging: %v, %d saves", err, puts)
	}
	if has, err := client.SeasonsHaveFiles(42, []int{1}); err != nil || !has {
		t.Fatalf("season 1: %v %v", has, err)
	}
	if has, _ := client.SeasonsHaveFiles(42, []int{1, 2}); has {
		t.Fatal("season 2 has no file")
	}
}

// A refresh still running holds it back; once over, it goes on.
func TestWaitAdded(t *testing.T) {
	var asked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := "started"
		if asked.Add(1) >= 3 {
			status = "completed"
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"name": "RefreshSeries", "status": "started", "body": map[string]any{"seriesIds": []int{99}}},
			{"name": "RssSync", "status": "started", "body": map[string]any{}},
			{"name": "RefreshSeries", "status": status, "body": map[string]any{"seriesIds": []int{7}}},
		})
	}))
	defer server.Close()
	New(server.URL, "k").WaitAdded(7)
	if asked.Load() != 3 {
		t.Errorf("asked %d times, want until it completed, 3", asked.Load())
	}
}

// A Sonarr that can't say doesn't hold anything up.
func TestWaitAddedUnknown(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	started := time.Now()
	New(server.URL, "k").WaitAdded(7)
	if time.Since(started) > time.Second {
		t.Errorf("waited %s", time.Since(started))
	}
}
