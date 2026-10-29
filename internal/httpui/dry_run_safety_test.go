package httpui

import (
	"context"
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

// In dry run, a removal changes nothing: no service is asked to delete or
// change anything, and no file leaves the disk, whatever kind of removal
// it is and whatever the form asks.
func TestDryRunRemovesNothing(t *testing.T) {
	var mu sync.Mutex
	var changes []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signing in to qBittorrent is a POST that changes nothing.
		if r.Method != http.MethodGet && r.URL.Path != "/api/v2/auth/login" {
			mu.Lock()
			changes = append(changes, r.Method+" "+r.URL.Path)
			mu.Unlock()
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
	movie, torrentFile, loose := file("movie.mkv"), file("release.mkv"), file("loose.mkv")

	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	mediaRefs := []model.MediaFileRef{{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 10, Path: movie.Path}}
	torrentRefs := []model.TorrentFileRef{{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: "oldhash", Path: torrentFile.Path}}
	unmanaged := []model.UnmanagedFile{{Path: loose.Path, SizeBytes: 4, Device: loose.Device, Inode: loose.Inode, Links: loose.Links}}
	torrents := []model.Torrent{{Hash: "oldhash", Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader"}}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1", ServiceName: "Movies"}}
	if err := database.PublishReconciliation(1, []model.File{movie, torrentFile, loose}, mediaRefs, torrentRefs, unmanaged, torrents, media); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Removal: config.RemovalConfig{DryRun: true}, Services: []config.Service{
		{ID: "radarr-1", Type: "radarr", Name: "Movies", URL: fake.URL, APIKey: "k", AllowAutomaticRemoval: true},
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: fake.URL, AllowAutomaticRemoval: true},
	}}
	server := &Server{inv: inventory.New(cfg, database)}

	for _, form := range []url.Values{
		{"kind": {"media"}, "media_type": {"movie"}, "media_id": {"1"}, "service_id": {"radarr-1"}, "managed_file": {managedFileKey(mediaRefs[0])}},
		{"kind": {"torrent"}, "hash": {"oldhash"}, "service_id": {"qbittorrent-1"}, "target": {"1"}},
		{"kind": {"unmanaged"}, "path": {loose.Path}},
		// The form can't turn dry run off.
		{"kind": {"torrent"}, "hash": {"oldhash"}, "service_id": {"qbittorrent-1"}, "target": {"1"}, "_dry_run": {"0"}},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.ParseForm()
		w := httptest.NewRecorder()
		server.executeRemovalNowContext(w, r, context.Background())
		// It ran to the end (and was turned away by nothing else).
		if w.Code != http.StatusSeeOther {
			t.Errorf("%s: %d %s", form.Get("kind"), w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(changes) > 0 {
		t.Errorf("dry run asked services to change things: %v", changes)
	}
	for _, f := range []model.File{movie, torrentFile, loose} {
		if _, err := os.Stat(f.Path); err != nil {
			t.Errorf("dry run removed %s: %v", f.Path, err)
		}
	}
}
