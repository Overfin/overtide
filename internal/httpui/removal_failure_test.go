package httpui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// A removal for real whose service refuses: it ends failed, says why,
// leaves the file where it was, and goes no further (no unmonitoring or
// tagging of a title whose file is still there).
func TestRemovalWhenTheServiceRefuses(t *testing.T) {
	var mu sync.Mutex
	var changes []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.URL.Path != "/api/v2/auth/login" {
			mu.Lock()
			changes = append(changes, r.Method+" "+r.URL.Path)
			mu.Unlock()
		}
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s"})
			w.Write([]byte("Ok."))
			return
		}
		if r.Method == http.MethodDelete || r.URL.Path == "/api/v2/torrents/delete" {
			http.Error(w, "refused", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer fake.Close()

	dir := t.TempDir()
	file := func(name string) model.File {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		return model.File{Path: p, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink)}
	}
	movie, torrentFile := file("movie.mkv"), file("release.mkv")
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	mediaRefs := []model.MediaFileRef{{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 10, Path: movie.Path}}
	torrentRefs := []model.TorrentFileRef{{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: "0123456789abcdef0123456789abcdef01234567", Path: torrentFile.Path}}
	torrents := []model.Torrent{{Hash: "0123456789abcdef0123456789abcdef01234567", Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader"}}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1", ServiceName: "Movies"}}
	if err := database.PublishReconciliation(1, []model.File{movie, torrentFile}, mediaRefs, torrentRefs, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Removal: config.RemovalConfig{DryRun: false}, Services: []config.Service{
		{ID: "radarr-1", Type: "radarr", Name: "Movies", URL: fake.URL, APIKey: "k", AllowAutomaticRemoval: true},
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: fake.URL, AllowAutomaticRemoval: true},
	}}
	server := &Server{inv: inventory.New(cfg, database)}

	for _, form := range []url.Values{
		{"kind": {"media"}, "media_type": {"movie"}, "media_id": {"1"}, "service_id": {"radarr-1"}, "managed_file": {managedFileKey(mediaRefs[0])}},
		{"kind": {"torrent"}, "hash": {"0123456789abcdef0123456789abcdef01234567"}, "service_id": {"qbittorrent-1"}, "target": {"1"}},
	} {
		mu.Lock()
		changes = nil
		mu.Unlock()
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Overtide-Overlay", "1")
		r.ParseForm()
		w := httptest.NewRecorder()
		server.executeRemovalNowContext(w, r, context.Background())
		var out struct {
			Status  string
			DryRun  bool
			Results []string
			Errors  []string
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s: %d %s", form.Get("kind"), w.Code, w.Body)
		}
		if out.Status != "failed" || out.DryRun || len(out.Errors) == 0 || len(out.Results) != 0 {
			t.Errorf("%s: %+v", form.Get("kind"), out)
		}
		mu.Lock()
		for _, c := range changes {
			if !strings.HasPrefix(c, "DELETE ") && c != "POST /api/v2/torrents/delete" {
				t.Errorf("%s: went on after the refusal: %s", form.Get("kind"), c)
			}
		}
		if len(changes) == 0 {
			t.Errorf("%s: never asked the service", form.Get("kind"))
		}
		mu.Unlock()
	}
	for _, f := range []model.File{movie, torrentFile} {
		if _, err := os.Stat(f.Path); err != nil {
			t.Errorf("%s gone: %v", f.Path, err)
		}
	}
	events, err := database.HistoryEvents(10)
	if err != nil || len(events) != 2 {
		t.Fatalf("history: %d events, %v", len(events), err)
	}
	for _, e := range events {
		if e.EventType != "removal" || e.Status != "failed" || e.Error == "" {
			t.Errorf("history: %s %s %q", e.EventType, e.Status, e.Error)
		}
	}
}
