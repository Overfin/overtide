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

// scheduledFixture is a live Overtide (dry run off) with one film and one
// old torrent on disk, fake services that record every change asked, and
// the forms that remove each. run queues a removal and has the scheduler
// run it.
type scheduledFixture struct {
	t        *testing.T
	server   *Server
	database *store.Store
	files    []model.File
	forms    []url.Values
	mu       sync.Mutex
	changes  []string
}

func newScheduledFixture(t *testing.T, torrent model.Torrent, kept ...config.KeptItem) *scheduledFixture {
	fx := &scheduledFixture{t: t}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s"})
			w.Write([]byte("Ok."))
			return
		}
		if r.Method != http.MethodGet {
			fx.mu.Lock()
			fx.changes = append(fx.changes, r.Method+" "+r.URL.Path)
			fx.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	t.Cleanup(fake.Close)

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
	fx.files = []model.File{movie, torrentFile}
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	fx.database = database
	mediaRefs := []model.MediaFileRef{{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 10, Path: movie.Path}}
	torrentRefs := []model.TorrentFileRef{{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: torrent.Hash, Path: torrentFile.Path}}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1", ServiceName: "Movies"}}
	if err := database.PublishReconciliation(1, fx.files, mediaRefs, torrentRefs, nil, []model.Torrent{torrent}, media); err != nil {
		t.Fatal(err)
	}
	// Dry run is off now, whatever it was when the removals were queued.
	cfg := config.Config{Removal: config.RemovalConfig{DryRun: false}, Services: []config.Service{
		{ID: "radarr-1", Type: "radarr", Name: "Movies", URL: fake.URL, APIKey: "k", AllowAutomaticRemoval: true},
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: fake.URL, AllowAutomaticRemoval: true},
	}, Kept: kept}
	fx.server = &Server{inv: inventory.New(cfg, database)}
	fx.forms = []url.Values{
		{"kind": {"media"}, "media_type": {"movie"}, "media_id": {"1"}, "service_id": {"radarr-1"}, "managed_file": {managedFileKey(mediaRefs[0])}},
		{"kind": {"torrent"}, "hash": {torrent.Hash}, "service_id": {"qbittorrent-1"}, "target": {"1"}},
	}
	return fx
}

// run queues command's removal in the history, has the scheduler run it, and
// returns its history event, the scheduler's error, and the changes asked.
func (fx *scheduledFixture) run(command scheduledRemovalCommand) (store.HistoryEvent, error, []string) {
	fx.t.Helper()
	fx.mu.Lock()
	fx.changes = nil
	fx.mu.Unlock()
	id, err := fx.database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "queued", DryRun: command.DryRun, RequestedKind: command.Form.Get("kind")})
	if err != nil {
		fx.t.Fatal(err)
	}
	command.HistoryID = id
	payload, err := json.Marshal(command)
	if err != nil {
		fx.t.Fatal(err)
	}
	runErr := fx.server.runScheduledRemoval(context.Background(), payload)
	event, err := fx.database.HistoryEventByID(id)
	if err != nil {
		fx.t.Fatal(err)
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return event, runErr, append([]string(nil), fx.changes...)
}

func (fx *scheduledFixture) filesStay(what string) {
	fx.t.Helper()
	for _, f := range fx.files {
		if _, err := os.Stat(f.Path); err != nil {
			fx.t.Fatalf("%s: %s gone", what, f.Path)
		}
	}
}

const scheduledHash = "0123456789abcdef0123456789abcdef01234567"

// A removal queued in dry run and run by the scheduler after dry run was
// turned off stays a dry run: no service is asked to change anything and
// the files stay. The same removal queued live does ask, which shows the
// test would see a change.
func TestSchedulerRunsAQueuedDryRunDry(t *testing.T) {
	fx := newScheduledFixture(t, model.Torrent{Hash: scheduledHash, Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader"})
	for _, form := range fx.forms {
		event, err, changes := fx.run(scheduledRemovalCommand{Form: form, DryRun: true})
		if err != nil || !event.DryRun || event.Status == "failed" {
			t.Errorf("%s: history %s, dry run %v, error %v", form.Get("kind"), event.Status, event.DryRun, err)
		}
		if len(changes) != 0 {
			t.Errorf("%s queued in dry run asked for changes: %v", form.Get("kind"), changes)
		}
		fx.filesStay(form.Get("kind") + " queued in dry run")
	}
	for _, form := range fx.forms {
		// A live removal against the fake may fail; only what it asked matters.
		event, _, changes := fx.run(scheduledRemovalCommand{Form: form})
		if len(changes) == 0 {
			t.Errorf("%s queued live asked for nothing (history %s, dry run %v)", form.Get("kind"), event.Status, event.DryRun)
		}
	}
}

// A live removal queued from the cleanup plan whose torrent must now stay
// (here, kept by the operator since) is refused before any service is
// asked. Without the cleanup mark the same removal does ask the service,
// which shows the refusal is the recheck's.
func TestSchedulerRechecksACleanupRemoval(t *testing.T) {
	fx := newScheduledFixture(t, model.Torrent{Hash: scheduledHash, Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader", AddedOn: 1_600_000_000}, config.KeptItem{Key: config.KeptTorrentKey(scheduledHash)})
	torrent := fx.forms[1]
	event, err, changes := fx.run(scheduledRemovalCommand{Form: torrent, Cleanup: true})
	if err == nil || event.Status != "failed" || !strings.Contains(event.Error, "No longer a cleanup candidate") || !strings.Contains(event.Error, "Old Release: Kept") {
		t.Errorf("history %s %q, error %v", event.Status, event.Error, err)
	}
	if len(changes) != 0 {
		t.Errorf("asked for changes: %v", changes)
	}
	fx.filesStay("cleanup removal of a torrent that must stay")
	if _, _, changes := fx.run(scheduledRemovalCommand{Form: torrent}); len(changes) == 0 {
		t.Errorf("queued by hand, the removal asked for nothing")
	}
}
