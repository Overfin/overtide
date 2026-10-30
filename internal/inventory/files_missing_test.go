package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// Torrents whose files are gone leave qBittorrent only when every file is
// still absent and their storage root has something in it (an empty one
// may be a disk that isn't mounted), and never when kept or restricted.
// Their files are always kept; in dry run qBittorrent is asked nothing.
func TestForgetMissingTorrents(t *testing.T) {
	var mu sync.Mutex
	var deletes []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/delete":
			r.ParseForm()
			mu.Lock()
			deletes = append(deletes, r.Form.Get("hashes")+" deleteFiles="+r.Form.Get("deleteFiles"))
			mu.Unlock()
		default:
			w.Write([]byte("[]"))
		}
	}))
	defer fake.Close()

	full, empty := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "other.mkv"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	back := filepath.Join(full, "back.mkv")
	if err := os.WriteFile(back, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	hash := func(c byte) string { return strings.Repeat(string(c), 40) }
	torrent := func(c byte) model.Torrent {
		return model.Torrent{Hash: hash(c), Name: string(c), ServiceID: "qb", FilesMissing: true}
	}
	gone, onEmpty, isBack, outside, kept, restricted, noFiles := torrent('a'), torrent('b'), torrent('c'), torrent('d'), torrent('e'), torrent('f'), torrent('0')
	kept.Obligation = "Kept"
	restricted.RemovalRestricted = true
	ref := func(tt model.Torrent, path string) model.TorrentFileRef {
		return model.TorrentFileRef{ServiceID: "qb", Hash: tt.Hash, Path: path}
	}
	refs := []model.TorrentFileRef{
		ref(gone, filepath.Join(full, "gone.mkv")),
		ref(onEmpty, filepath.Join(empty, "gone.mkv")),
		ref(isBack, back),
		ref(outside, "/elsewhere/gone.mkv"),
		ref(kept, filepath.Join(full, "kept.mkv")),
		ref(restricted, filepath.Join(full, "restricted.mkv")),
	}

	for _, dryRun := range []bool{true, false} {
		mu.Lock()
		deletes = nil
		mu.Unlock()
		database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := config.Config{Removal: config.RemovalConfig{DryRun: dryRun}, Services: []config.Service{{ID: "qb", Type: "qbittorrent", Name: "Downloader", URL: fake.URL}}}
		service := New(cfg, database)
		service.mu.Lock()
		service.torrents = []model.Torrent{gone, onEmpty, isBack, outside, kept, restricted, noFiles}
		service.torrentFileRefs = refs
		service.storageRoots = []storageRoot{{Path: full}, {Path: empty}}
		service.mu.Unlock()

		removed := map[string]bool{}
		for _, result := range service.ForgetMissingTorrents(context.Background()) {
			if result.Removed {
				removed[result.Torrent.Name] = true
			} else if result.Reason == "" {
				t.Errorf("dry run %v: %s left without a reason", dryRun, result.Torrent.Name)
			}
		}
		if len(removed) != 1 || !removed["a"] {
			t.Errorf("dry run %v: removed %v, want only a", dryRun, removed)
		}
		mu.Lock()
		switch {
		case dryRun && len(deletes) != 0:
			t.Errorf("dry run asked qBittorrent: %v", deletes)
		case !dryRun && (len(deletes) != 1 || deletes[0] != hash('a')+" deleteFiles=false"):
			t.Errorf("live: asked %v", deletes)
		}
		mu.Unlock()
		database.Close()
	}
	if _, err := os.Stat(back); err != nil {
		t.Fatalf("a file on disk went: %v", err)
	}
}
