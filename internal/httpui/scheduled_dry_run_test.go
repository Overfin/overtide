package httpui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// A removal queued in dry run and run by the scheduler after dry run was
// turned off stays a dry run: no service is asked to change anything and
// the files stay. The same removal queued live does ask, which shows the
// test would see a change.
func TestSchedulerRunsAQueuedDryRunDry(t *testing.T) {
	var mu sync.Mutex
	var changes []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s"})
			w.Write([]byte("Ok."))
			return
		}
		if r.Method != http.MethodGet {
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
	movie, torrentFile := file("movie.mkv"), file("release.mkv")
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	const hash = "0123456789abcdef0123456789abcdef01234567"
	mediaRefs := []model.MediaFileRef{{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 10, Path: movie.Path}}
	torrentRefs := []model.TorrentFileRef{{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: hash, Path: torrentFile.Path}}
	torrents := []model.Torrent{{Hash: hash, Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader"}}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1", ServiceName: "Movies"}}
	if err := database.PublishReconciliation(1, []model.File{movie, torrentFile}, mediaRefs, torrentRefs, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	// Dry run is off now, whatever it was when the removals were queued.
	cfg := config.Config{Removal: config.RemovalConfig{DryRun: false}, Services: []config.Service{
		{ID: "radarr-1", Type: "radarr", Name: "Movies", URL: fake.URL, APIKey: "k", AllowAutomaticRemoval: true},
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: fake.URL, AllowAutomaticRemoval: true},
	}}
	server := &Server{inv: inventory.New(cfg, database)}

	forms := []url.Values{
		{"kind": {"media"}, "media_type": {"movie"}, "media_id": {"1"}, "service_id": {"radarr-1"}, "managed_file": {managedFileKey(mediaRefs[0])}},
		{"kind": {"torrent"}, "hash": {hash}, "service_id": {"qbittorrent-1"}, "target": {"1"}},
	}
	run := func(form url.Values, dryRun bool) store.HistoryEvent {
		t.Helper()
		id, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "queued", DryRun: dryRun, RequestedKind: form.Get("kind")})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(scheduledRemovalCommand{HistoryID: id, Form: form, DryRun: dryRun})
		if err != nil {
			t.Fatal(err)
		}
		// A live removal against the fake may fail; only what it asked matters.
		runErr := server.runScheduledRemoval(context.Background(), payload)
		event, err := database.HistoryEventByID(id)
		if err != nil {
			t.Fatal(err)
		}
		if dryRun && runErr != nil {
			t.Fatalf("%s in dry run: %v", form.Get("kind"), runErr)
		}
		return event
	}

	for _, form := range forms {
		mu.Lock()
		changes = nil
		mu.Unlock()
		event := run(form, true)
		if !event.DryRun || event.Status == "failed" {
			t.Errorf("%s: history %s, dry run %v", form.Get("kind"), event.Status, event.DryRun)
		}
		mu.Lock()
		if len(changes) != 0 {
			t.Errorf("%s queued in dry run asked for changes: %v", form.Get("kind"), changes)
		}
		mu.Unlock()
		for _, f := range []model.File{movie, torrentFile} {
			if _, err := os.Stat(f.Path); err != nil {
				t.Fatalf("%s queued in dry run: %s gone", form.Get("kind"), f.Path)
			}
		}
	}

	for _, form := range forms {
		mu.Lock()
		changes = nil
		mu.Unlock()
		event := run(form, false)
		mu.Lock()
		if len(changes) == 0 {
			t.Errorf("%s queued live asked for nothing (history %s, dry run %v)", form.Get("kind"), event.Status, event.DryRun)
		}
		mu.Unlock()
	}
}
