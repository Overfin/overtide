package radarr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Adding sends the lookup back with the folder and profile, tagged,
// unmonitored, and without searching; tagging everything is one call; a
// grab is seen in the history.
func TestAddingCalls(t *testing.T) {
	var added map[string]any
	var editor map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/movie/lookup/tmdb":
			_, _ = w.Write([]byte(`{"title":"Film","year":2026,"tmdbId":5,"digitalRelease":"2026-09-01T00:00:00Z","images":[]}`))
		case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&added)
			_, _ = w.Write([]byte(`{"id":42}`))
		case r.URL.Path == "/api/v3/movie" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[{"id":1},{"id":42}]`))
		case r.URL.Path == "/api/v3/movie/editor" && r.Method == http.MethodPut:
			_ = json.NewDecoder(r.Body).Decode(&editor)
		case r.URL.Path == "/api/v3/queue/details":
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/v3/history/movie":
			_, _ = w.Write([]byte(`[{"eventType":"grabbed","date":"2026-10-25T12:00:00Z"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := New(server.URL, "key")

	lookup, err := client.LookupMovie(5)
	if err != nil || lookup.DigitalRelease == nil || lookup.Title != "Film" {
		t.Fatalf("lookup: %#v %v", lookup, err)
	}
	id, err := client.AddMovie(lookup, "/data/Films", 7, 3)
	if err != nil || id != 42 {
		t.Fatalf("add: %d %v", id, err)
	}
	options, _ := added["addOptions"].(map[string]any)
	if added["rootFolderPath"] != "/data/Films" || added["qualityProfileId"] != float64(7) || added["monitored"] != false || options["searchForMovie"] != false || added["title"] != "Film" {
		t.Fatalf("added with %#v", added)
	}
	if tags, _ := added["tags"].([]any); len(tags) != 1 || tags[0] != float64(3) {
		t.Fatalf("tags: %#v", added["tags"])
	}
	before, _ := time.Parse(time.RFC3339, "2026-10-25T11:00:00Z")
	after, _ := time.Parse(time.RFC3339, "2026-10-25T13:00:00Z")
	if grabbed, err := client.GrabbedSince(42, before); err != nil || !grabbed {
		t.Fatalf("grab since before: %v %v", grabbed, err)
	}
	if grabbed, _ := client.GrabbedSince(42, after); grabbed {
		t.Fatal("an older grab counted")
	}
	if n, err := client.TagAll(3); err != nil || n != 2 || editor["applyTags"] != "add" || len(editor["movieIds"].([]any)) != 2 {
		t.Fatalf("tag all: %d %v %#v", n, err, editor)
	}
}

// A title goes to Radarr's root folder with its quality profile; with
// several of either, Overtide doesn't pick one.
func TestDestination(t *testing.T) {
	folders := `[{"path":"/data/Films"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/rootfolder":
			_, _ = w.Write([]byte(folders))
		case "/api/v3/qualityprofile":
			_, _ = w.Write([]byte(`[{"id":7,"name":"Default"}]`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	client := New(server.URL, "key")
	if root, profile, err := client.Destination(); err != nil || root != "/data/Films" || profile != 7 {
		t.Fatalf("%q %d %v", root, profile, err)
	}
	folders = `[{"path":"/data/Films"},{"path":"/data/Other"}]`
	if _, _, err := client.Destination(); err == nil {
		t.Fatal("picked one of two root folders")
	}
}
