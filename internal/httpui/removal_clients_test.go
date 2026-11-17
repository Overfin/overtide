package httpui

import (
	"context"
	"fmt"
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

// fakeClient is a qBittorrent client seeding one torrent, at path.
type fakeClient struct {
	mu      sync.Mutex
	hash    string
	path    string
	deletes []string
	server  *httptest.Server
}

func newFakeClient(t *testing.T, hash, path string) *fakeClient {
	client := &fakeClient{hash: hash, path: path}
	client.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		client.mu.Lock()
		defer client.mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/delete":
			client.deletes = append(client.deletes, r.Form.Get("hashes")+" deleteFiles="+r.Form.Get("deleteFiles"))
			client.hash = ""
		case "/api/v2/torrents/info":
			w.Header().Set("Content-Type", "application/json")
			if client.hash == "" || (r.Form.Get("hashes") != "" && r.Form.Get("hashes") != client.hash) {
				w.Write([]byte("[]"))
				return
			}
			fmt.Fprintf(w, `[{"hash":%q,"name":"Release.mkv","save_path":%q,"content_path":%q,"progress":1}]`, client.hash, filepath.Dir(client.path), client.path)
		case "/api/v2/torrents/files":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"index":0,"name":"Release.mkv","size":4,"progress":1}]`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		}
	}))
	t.Cleanup(client.server.Close)
	return client
}

func (client *fakeClient) deleted() []string {
	client.mu.Lock()
	defer client.mu.Unlock()
	return append([]string(nil), client.deletes...)
}

// The same torrent (the same hash) in two qBittorrent clients, seeding
// the very same file: removing it from one is that client's alone. The
// file stays for the other, which is told nothing, and a removal naming a
// bare hash, as one queued before keys had clients does, is refused rather
// than guessed.
func TestRemovalTellsClientsApart(t *testing.T) {
	const hash = "dddddddddddddddddddddddddddddddddddddddd"
	dir := t.TempDir()
	path := filepath.Join(dir, "Release.mkv")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(path)
	st := info.Sys().(*syscall.Stat_t)
	file := model.File{Path: path, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink)}
	home, seedbox := newFakeClient(t, hash, path), newFakeClient(t, hash, path)

	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := config.Config{Services: []config.Service{
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Home", URL: home.server.URL, AllowAutomaticRemoval: true},
		{ID: "qbittorrent-2", Type: "qbittorrent", Name: "Seedbox", URL: seedbox.server.URL, AllowAutomaticRemoval: true},
	}}
	refs := []model.TorrentFileRef{
		{ServiceID: "qbittorrent-1", Client: "Home", Hash: hash, Path: path},
		{ServiceID: "qbittorrent-2", Client: "Seedbox", Hash: hash, Path: path},
	}
	torrents := []model.Torrent{
		{Hash: hash, Name: "Release", AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-1", Client: "Home"},
		{Hash: hash, Name: "Release", AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-2", Client: "Seedbox"},
	}
	if err := database.PublishReconciliation(1, []model.File{file}, nil, refs, nil, torrents, nil); err != nil {
		t.Fatal(err)
	}
	server := &Server{inv: inventory.New(cfg, database)}

	// The plan: the seedbox's torrent owns the file too, and isn't removed.
	plan, err := server.buildTorrentRemovalPlan(hash, "qbittorrent-1", true, map[string]bool{}, map[string]bool{}, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Plan.Kept(path) || plan.Plan.SelectedPathBytes != 0 {
		t.Fatalf("the other client's torrent was taken along: kept %v, freed %d", plan.Plan.Kept(path), plan.Plan.SelectedPathBytes)
	}
	if files, _, _ := server.inv.TorrentFiles("qbittorrent-2", hash); len(files) != 1 {
		t.Fatalf("a client's torrent files: %+v", files)
	}

	run := func(form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Overtide-Overlay", "1")
		r.ParseForm()
		w := httptest.NewRecorder()
		server.executeRemovalNowContext(w, r, context.Background())
		return w
	}

	// A bare hash in both clients: refused, nothing asked of either.
	legacy := url.Values{"kind": {"media"}, "media_type": {"movie"}, "media_id": {"1"}, "managed_file": {"radarr:1"}, "torrent": {hash}}
	if w := run(legacy); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "more than one torrent client") {
		t.Fatalf("an ambiguous bare hash: %d %s", w.Code, w.Body)
	}

	// Removing the home client's: without its data, the seedbox untouched.
	w := run(url.Values{"kind": {"torrent"}, "hash": {hash}, "service_id": {"qbittorrent-1"}, "target": {"1"}})
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := home.deleted(); len(got) != 1 || got[0] != hash+" deleteFiles=false" {
		t.Fatalf("home client asked: %v", got)
	}
	if got := seedbox.deleted(); len(got) != 0 {
		t.Fatalf("the seedbox was asked to remove its torrent: %v", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the seedbox's file is gone: %v", err)
	}
}

// A removal's torrent values are client|hash keys; a bare hash, from a
// removal queued before, is its torrent only when one client has it.
func TestKeyTorrentFields(t *testing.T) {
	torrents := []model.Torrent{{Hash: "AAA", ServiceID: "qb-1"}, {Hash: "bbb", ServiceID: "qb-1"}, {Hash: "bbb", ServiceID: "qb-2"}}
	form := url.Values{"torrent": {"aaa", "qb-2|BBB"}}
	if err := keyTorrentFields(form, torrents); err != nil || strings.Join(form["torrent"], ",") != "qb-1|aaa,qb-2|bbb" {
		t.Fatalf("%v %v", form, err)
	}
	if err := keyTorrentFields(url.Values{"torrent": {"bbb"}}, torrents); err == nil {
		t.Fatal("a bare hash in two clients was resolved")
	}
	if err := keyTorrentFields(url.Values{"torrent": {"ccc"}}, torrents); err == nil {
		t.Fatal("an unknown bare hash was resolved")
	}
	if err := torrentsKnown([]string{"qb-3|aaa"}, torrents); err == nil {
		t.Fatal("a torrent of another client was taken as known")
	}
}

// The same torrent in two clients at the same path is one group: the
// window lists the other client's, named by its client, and removing both
// removes the file with the last of them.
func TestRemovalTakesTheOtherClientsTwinAlong(t *testing.T) {
	const hash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	dir := t.TempDir()
	path := filepath.Join(dir, "Release.mkv")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(path)
	st := info.Sys().(*syscall.Stat_t)
	file := model.File{Path: path, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink)}
	home, seedbox := newFakeClient(t, hash, path), newFakeClient(t, hash, path)
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := config.Config{Services: []config.Service{
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Home", URL: home.server.URL, AllowAutomaticRemoval: true},
		{ID: "qbittorrent-2", Type: "qbittorrent", Name: "Seedbox", URL: seedbox.server.URL, AllowAutomaticRemoval: true},
	}}
	refs := []model.TorrentFileRef{
		{ServiceID: "qbittorrent-1", Client: "Home", Hash: hash, Path: path},
		{ServiceID: "qbittorrent-2", Client: "Seedbox", Hash: hash, Path: path},
	}
	torrents := []model.Torrent{
		{Hash: hash, Name: "Release", AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-1", Client: "Home", SharesFilesWith: []string{"qbittorrent-2|" + hash}},
		{Hash: hash, Name: "Release", AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-2", Client: "Seedbox", SharesFilesWith: []string{"qbittorrent-1|" + hash}},
	}
	if err := database.PublishReconciliation(1, []model.File{file}, nil, refs, nil, torrents, nil); err != nil {
		t.Fatal(err)
	}
	server := &Server{inv: inventory.New(cfg, database)}

	plan, err := server.buildTorrentRemovalPlan(hash, "qbittorrent-1", true, map[string]bool{}, map[string]bool{}, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Related) != 1 || plan.Related[0].Torrent.ServiceID != "qbittorrent-2" || !strings.HasPrefix(relatedTorrentMeta(plan.Related[0]), "in Seedbox") {
		t.Fatalf("the seedbox's twin isn't offered: %+v", plan.Related)
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"kind": {"torrent"}, "hash": {hash}, "service_id": {"qbittorrent-1"}, "target": {"1"}, "torrent": {"qbittorrent-2|" + hash}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Overtide-Overlay", "1")
	r.ParseForm()
	w := httptest.NewRecorder()
	server.executeRemovalNowContext(w, r, context.Background())
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// One at a time: the first goes without the file the other still
	// uses, the last takes it (qBittorrent deletes it, asked with
	// deleteFiles).
	asked := append(home.deleted(), seedbox.deleted()...)
	if len(asked) != 2 || asked[0] != hash+" deleteFiles=false" || asked[1] != hash+" deleteFiles=true" {
		t.Fatalf("home asked %v, seedbox %v", home.deleted(), seedbox.deleted())
	}
}

// Just before deleting, every qBittorrent client is asked who uses the
// files, not only the torrent's own: one another client started seeding
// from the same path since the last refresh keeps them.
func TestRemovalAsksEveryClientWhoUsesTheFiles(t *testing.T) {
	const hash, other = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	dir := t.TempDir()
	path := filepath.Join(dir, "Release.mkv")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(path)
	st := info.Sys().(*syscall.Stat_t)
	file := model.File{Path: path, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink)}
	// The seedbox seeds the same file now; the last refresh didn't see it.
	home, seedbox := newFakeClient(t, hash, path), newFakeClient(t, other, path)
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := config.Config{Services: []config.Service{
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Home", URL: home.server.URL, AllowAutomaticRemoval: true},
		{ID: "qbittorrent-2", Type: "qbittorrent", Name: "Seedbox", URL: seedbox.server.URL, AllowAutomaticRemoval: true},
	}}
	refs := []model.TorrentFileRef{{ServiceID: "qbittorrent-1", Client: "Home", Hash: hash, Path: path}}
	torrents := []model.Torrent{{Hash: hash, Name: "Release", AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-1", Client: "Home"}}
	if err := database.PublishReconciliation(1, []model.File{file}, nil, refs, nil, torrents, nil); err != nil {
		t.Fatal(err)
	}
	server := &Server{inv: inventory.New(cfg, database)}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"kind": {"torrent"}, "hash": {hash}, "service_id": {"qbittorrent-1"}, "target": {"1"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Overtide-Overlay", "1")
	r.ParseForm()
	w := httptest.NewRecorder()
	server.executeRemovalNowContext(w, r, context.Background())
	if got := home.deleted(); len(got) != 1 || got[0] != hash+" deleteFiles=false" {
		t.Fatalf("home asked %v (%d %s): the seedbox's file would go", got, w.Code, w.Body)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the seedbox's file is gone: %v", err)
	}
}

// A file the plan already keeps (another torrent uses it) needs no client
// asked: a client out of reach doesn't hold back a removal that deletes
// nothing.
func TestRemovalKeepingEverythingDoesntWaitForAClientDown(t *testing.T) {
	const hash = "cccccccccccccccccccccccccccccccccccccccc"
	dir := t.TempDir()
	path := filepath.Join(dir, "Release.mkv")
	if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Lstat(path)
	st := info.Sys().(*syscall.Stat_t)
	file := model.File{Path: path, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink)}
	home := newFakeClient(t, hash, path)
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := config.Config{Services: []config.Service{
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Home", URL: home.server.URL, AllowAutomaticRemoval: true},
		// Out of reach.
		{ID: "qbittorrent-2", Type: "qbittorrent", Name: "Seedbox", URL: "http://127.0.0.1:1", AllowAutomaticRemoval: true},
	}}
	refs := []model.TorrentFileRef{
		{ServiceID: "qbittorrent-1", Client: "Home", Hash: hash, Path: path},
		{ServiceID: "qbittorrent-2", Client: "Seedbox", Hash: hash, Path: path},
	}
	torrents := []model.Torrent{
		{Hash: hash, Name: "Release", AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-1", Client: "Home", SharesFilesWith: []string{"qbittorrent-2|" + hash}},
		{Hash: hash, Name: "Release", AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-2", Client: "Seedbox", SharesFilesWith: []string{"qbittorrent-1|" + hash}},
	}
	if err := database.PublishReconciliation(1, []model.File{file}, nil, refs, nil, torrents, nil); err != nil {
		t.Fatal(err)
	}
	server := &Server{inv: inventory.New(cfg, database)}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(url.Values{"kind": {"torrent"}, "hash": {hash}, "service_id": {"qbittorrent-1"}, "target": {"1"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Overtide-Overlay", "1")
	r.ParseForm()
	w := httptest.NewRecorder()
	server.executeRemovalNowContext(w, r, context.Background())
	if got := home.deleted(); len(got) != 1 || got[0] != hash+" deleteFiles=false" {
		t.Fatalf("home asked %v: %d %s", got, w.Code, w.Body)
	}
}
