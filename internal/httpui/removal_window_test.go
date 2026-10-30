package httpui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// The removal window opens for a film and for a torrent, naming what it
// would remove, and says so when the title or torrent is unknown.
func TestRemovalWindowOpens(t *testing.T) {
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
	torrentRefs := []model.TorrentFileRef{{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: scheduledHash, Path: torrentFile.Path}}
	torrents := []model.Torrent{{Hash: scheduledHash, Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader", AddedOn: 1_600_000_000}}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1", ServiceName: "Movies"}}
	if err := database.PublishReconciliation(1, []model.File{movie, torrentFile}, mediaRefs, torrentRefs, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Removal: config.RemovalConfig{DryRun: true}, Services: []config.Service{
		{ID: "radarr-1", Type: "radarr", Name: "Movies", URL: "http://127.0.0.1:1", APIKey: "k"},
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: "http://127.0.0.1:1"},
	}}
	server, err := New(inventory.New(cfg, database), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()
	for _, tc := range []struct {
		path, want string
		status     int
	}{
		{"/removal/media?type=movie&id=1&service_id=radarr-1", "Test Movie", http.StatusOK},
		{"/removal/torrent?hash=" + scheduledHash + "&service_id=qbittorrent-1", "Old Release", http.StatusOK},
		{"/removal/media?type=movie&id=99&service_id=radarr-1", "", http.StatusNotFound},
		{"/removal/torrent?hash=" + strings.Repeat("f", 40) + "&service_id=qbittorrent-1", "", http.StatusNotFound},
	} {
		request := httptest.NewRequest(http.MethodGet, tc.path, nil)
		request.Header.Set("X-Overtide-Overlay", "1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.want) {
			t.Errorf("%s: %d, %.300s", tc.path, response.Code, response.Body.String())
		}
	}
}
