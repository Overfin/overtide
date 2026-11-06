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
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// As on 2026-11-02, the torrent client drops the connection at the
// removal's delete: the driver sends it again, and the removal ends a
// success, not partial.
func TestRemovalTriesTheTorrentAgain(t *testing.T) {
	dir := t.TempDir()
	torrentPath := filepath.Join(dir, "release.mkv")
	if err := os.WriteFile(torrentPath, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(torrentPath)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	torrentFile := model.File{Path: torrentPath, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink)}
	hash := "0123456789abcdef0123456789abcdef01234567"
	var deletes atomic.Int32
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/delete":
			if deletes.Add(1) == 1 {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			os.Remove(torrentPath)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		}
	}))
	defer fake.Close()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	torrentRefs := []model.TorrentFileRef{{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: hash, Path: torrentPath}}
	torrents := []model.Torrent{{Hash: hash, Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader"}}
	if err := database.PublishReconciliation(1, []model.File{torrentFile}, nil, torrentRefs, nil, torrents, nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Removal: config.RemovalConfig{DryRun: false}, Services: []config.Service{
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: fake.URL, AllowAutomaticRemoval: true},
	}}
	server := &Server{inv: inventory.New(cfg, database)}
	form := url.Values{"kind": {"torrent"}, "hash": {hash}, "service_id": {"qbittorrent-1"}, "target": {"1"}}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Overtide-Overlay", "1")
	r.ParseForm()
	w := httptest.NewRecorder()
	server.executeRemovalNowContext(w, r, context.Background())
	var out struct {
		Status string
		Errors []string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if out.Status != "success" || len(out.Errors) != 0 || deletes.Load() != 2 {
		t.Fatalf("%+v after %d deletes", out, deletes.Load())
	}
}
