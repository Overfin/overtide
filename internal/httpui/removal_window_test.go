package httpui

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
	"github.com/overfin/overtide/internal/tasks"
)

// removalFixture is a signed-in Overtide with a film (two real files: the
// film's, and a superseded torrent's), in dry run, with a scheduler that
// queues removals but never runs them (it isn't started).
type removalFixture struct {
	handler  http.Handler
	server   *Server
	database *store.Store
	session  *http.Cookie
	movie    model.File
}

func newRemovalFixture(t *testing.T) *removalFixture {
	t.Helper()
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
	t.Cleanup(func() { database.Close() })
	mediaRefs := []model.MediaFileRef{{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 10, Path: movie.Path}}
	torrentRefs := []model.TorrentFileRef{{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: scheduledHash, Path: torrentFile.Path}}
	torrents := []model.Torrent{{Hash: scheduledHash, Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader", AddedOn: 1_600_000_000}}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1", ServiceName: "Movies"}}
	if err := database.PublishReconciliation(1, []model.File{movie, torrentFile}, mediaRefs, torrentRefs, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Removal.DryRun = true
	cfg.Services = []config.Service{
		{ID: "radarr-1", Type: "radarr", Name: "Movies", URL: "http://127.0.0.1:1", APIKey: "k"},
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: "http://127.0.0.1:1"},
	}
	inv := inventory.New(cfg, database)
	inv.SetConfigPath(configPath)
	manager, err := tasks.NewPersistent(database)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(inv, manager)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	return &removalFixture{handler: handler, server: server, database: database, session: setUp(t, handler), movie: movie}
}

// The removal window opens for a film and for a torrent, naming what it
// would remove, and says so when the title or torrent is unknown; signed
// out, it answers nothing.
func TestRemovalWindowOpens(t *testing.T) {
	fx := newRemovalFixture(t)
	for _, tc := range []struct {
		path, want string
		status     int
	}{
		{"/api/removal/media?type=movie&id=1&service_id=radarr-1", "Test Movie", http.StatusOK},
		{"/api/removal/torrent?hash=" + scheduledHash + "&service_id=qbittorrent-1", "Old Release", http.StatusOK},
		{"/api/removal/media?type=movie&id=99&service_id=radarr-1", `"error"`, http.StatusNotFound},
		{"/api/removal/torrent?hash=" + strings.Repeat("f", 40) + "&service_id=qbittorrent-1", `"error"`, http.StatusNotFound},
	} {
		response := getPath(t, fx.handler, tc.path, fx.session)
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.want) {
			t.Errorf("%s: %d, %.300s", tc.path, response.Code, response.Body.String())
		}
	}
	if response := getPath(t, fx.handler, "/api/removal/media?type=movie&id=1&service_id=radarr-1", nil); response.Code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", response.Code)
	}
	view := decode[removalView](t, getPath(t, fx.handler, "/api/removal/media?type=movie&id=1&service_id=radarr-1", fx.session))
	if !view.DryRun || view.OperationToken == "" || view.MediaID != 1 || view.MediaServiceID != "radarr-1" || len(view.Selection.Files) == 0 {
		t.Fatalf("media window: %+v", view)
	}
}
