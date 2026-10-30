package inventory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

func keepTestService(t *testing.T, body string) (*Service, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(cfg, nil)
	service.SetConfigPath(configPath)
	return service, configPath
}

// fakeRadarrTags is a Radarr keeping its tags and which film has which.
type fakeRadarrTags struct {
	mu     sync.Mutex
	labels map[int]string
	tagged map[int][]int // film → tag IDs
}

func (f *fakeRadarrTags) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/api/v3/tag" && r.Method == http.MethodGet:
		var out []map[string]any
		for id, label := range f.labels {
			out = append(out, map[string]any{"id": id, "label": label})
		}
		json.NewEncoder(w).Encode(out)
	case r.URL.Path == "/api/v3/tag":
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		id := len(f.labels) + 1
		f.labels[id] = body["label"]
		json.NewEncoder(w).Encode(map[string]any{"id": id})
	case r.URL.Path == "/api/v3/movie/editor":
		var body struct {
			MovieIDs  []int  `json:"movieIds"`
			Tags      []int  `json:"tags"`
			ApplyTags string `json:"applyTags"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		for _, film := range body.MovieIDs {
			for _, tag := range body.Tags {
				tags := slices.DeleteFunc(f.tagged[film], func(t int) bool { return t == tag })
				if body.ApplyTags == "add" {
					tags = append(tags, tag)
				}
				f.tagged[film] = tags
			}
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeRadarrTags) labelsOf(film int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, id := range f.tagged[film] {
		out = append(out, f.labels[id])
	}
	return out
}

// Keeping a title tags it overtide-keep in Radarr and protects it at once;
// nothing goes on the Kept list. Unkeeping undoes both.
func TestKeepProtectsAtOnceAndPersists(t *testing.T) {
	fake := &fakeRadarrTags{labels: map[int]string{}, tagged: map[int][]int{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	service, configPath := keepTestService(t, `{"storage":{},"services":[{"id":"r1","type":"radarr","name":"Movies","url":"`+srv.URL+`","api_key":"k"}]}`)
	service.items = []model.Media{{Type: model.Movie, ServiceID: "r1", SourceID: 42, Title: "Film"}}
	key := config.KeptMediaKey("movie", "r1", 42)

	if err := service.Keep(key, "Film (2020)"); err != nil {
		t.Fatal(err)
	}
	items, _, _ := service.Snapshot()
	if !items[0].Protected || items[0].ProtectionReason != "Kept" {
		t.Fatalf("expected the film protected at once: %#v", items[0])
	}
	if got := fake.labelsOf(42); !slices.Equal(got, []string{model.TagKeep}) {
		t.Fatalf("Radarr's tags: %v", got)
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.IsKept(key) {
		t.Fatalf("a title on the Kept list: %#v", reloaded.Kept)
	}

	if err := service.Unkeep(key); err != nil {
		t.Fatal(err)
	}
	items, _, _ = service.Snapshot()
	if items[0].ProtectionReason == "Kept" || len(fake.labelsOf(42)) != 0 {
		t.Fatalf("expected the film no longer kept: %#v, tags %v", items[0], fake.labelsOf(42))
	}
}

// Titles on the Kept list move to the overtide-keep tag once; torrents stay
// on the list.
func TestMigrateKept(t *testing.T) {
	fake := &fakeRadarrTags{labels: map[int]string{}, tagged: map[int][]int{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	service, configPath := keepTestService(t, `{"storage":{},"services":[{"id":"r1","type":"radarr","name":"Movies","url":"`+srv.URL+`","api_key":"k"}],
		"kept":[{"key":"movie:r1:7","name":"Film"},{"key":"torrent:abc","name":"Release"},{"key":"movie:gone:8","name":"Other"}]}`)
	service.items = []model.Media{{Type: model.Movie, ServiceID: "r1", SourceID: 7, Title: "Film"}}
	service.migrateKept()
	if got := fake.labelsOf(7); !slices.Equal(got, []string{model.TagKeep}) {
		t.Fatalf("Radarr's tags: %v", got)
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.IsKept("movie:r1:7") || !reloaded.IsKept("torrent:abc") || !reloaded.IsKept("movie:gone:8") {
		t.Fatalf("kept list: %#v", reloaded.Kept)
	}
	items, _, _ := service.Snapshot()
	if !items[0].Protected {
		t.Fatalf("not protected: %#v", items[0])
	}
}

// Old keep tags are brought into the Kept list once, then emptied.
func TestImportKeepTags(t *testing.T) {
	service, configPath := keepTestService(t, `{"storage":{},"protection":{"keep_tags":["keep"],"keep_torrent_tags":["stewarr_keep"]}}`)
	service.items = []model.Media{
		{Type: model.Movie, ServiceID: "r1", SourceID: 1, Title: "Tagged", Year: 2001, Tags: []string{"Keep"}},
		{Type: model.Movie, ServiceID: "r1", SourceID: 2, Title: "Untagged"},
	}
	service.torrents = []model.Torrent{{Hash: "AAA", Name: "Release", Tags: "x, stewarr_keep"}}

	service.importKeepTags()

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsKept(config.KeptMediaKey("movie", "r1", 1)) || reloaded.IsKept(config.KeptMediaKey("movie", "r1", 2)) || !reloaded.IsKept(config.KeptTorrentKey("aaa")) {
		t.Fatalf("kept: %#v", reloaded.Kept)
	}
	if reloaded.Kept[0].From != "keep tag keep" || reloaded.Kept[0].Name != "Tagged (2001)" {
		t.Fatalf("first item: %#v", reloaded.Kept[0])
	}
	if len(reloaded.Protection.KeepTags) != 0 || len(reloaded.Protection.KeepTorrentTags) != 0 {
		t.Fatalf("expected the tag lists emptied: %#v", reloaded.Protection)
	}
}
