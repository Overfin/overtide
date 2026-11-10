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

// fakeQB is a qBittorrent whose torrents are real files in one folder: a
// delete with data deletes them.
type fakeQB struct {
	mu       sync.Mutex
	dir      string
	torrents map[string][]string // hash → file names, relative to dir
	deletes  []string
	refuse   map[string]bool // hashes whose delete fails
	noFiles  map[string]bool // hashes whose file list fails
	server   *httptest.Server
}

func newFakeQB(t *testing.T, dir string) *fakeQB {
	f := &fakeQB{dir: dir, torrents: map[string][]string{}, refuse: map[string]bool{}, noFiles: map[string]bool{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/delete":
			h := r.Form.Get("hashes")
			f.deletes = append(f.deletes, h[:1]+" deleteFiles="+r.Form.Get("deleteFiles"))
			if f.refuse[h] {
				http.Error(w, "refused", http.StatusInternalServerError)
				return
			}
			if r.Form.Get("deleteFiles") == "true" {
				for _, name := range f.torrents[h] {
					os.Remove(filepath.Join(f.dir, name))
				}
			}
			delete(f.torrents, h)
		case "/api/v2/torrents/info":
			var xs []map[string]any
			for h, names := range f.torrents {
				if only := r.Form.Get("hashes"); only != "" && only != h {
					continue
				}
				content := f.dir
				if len(names) == 1 {
					content = filepath.Join(f.dir, names[0])
				}
				xs = append(xs, map[string]any{"hash": h, "name": names[0], "save_path": f.dir, "content_path": content, "progress": 1})
			}
			json.NewEncoder(w).Encode(xs)
		case "/api/v2/torrents/files":
			h := r.Form.Get("hash")
			if f.noFiles[h] {
				http.Error(w, "no", http.StatusInternalServerError)
				return
			}
			var xs []map[string]any
			for i, name := range f.torrents[h] {
				xs = append(xs, map[string]any{"index": i, "name": name, "size": 4, "progress": 1})
			}
			json.NewEncoder(w).Encode(xs)
		default:
			w.Write([]byte("[]"))
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

type sharedSetup struct {
	t        *testing.T
	dir      string
	qb       *fakeQB
	database *store.Store
	cfg      config.Config
}

func newSharedSetup(t *testing.T) *sharedSetup {
	dir := t.TempDir()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	qb := newFakeQB(t, dir)
	cfg := config.Config{Removal: config.RemovalConfig{DryRun: false}, Services: []config.Service{
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: qb.server.URL, AllowAutomaticRemoval: true},
	}}
	return &sharedSetup{t: t, dir: dir, qb: qb, database: database, cfg: cfg}
}

// write makes the files, and returns them as the inventory sees them.
func (s *sharedSetup) write(names ...string) map[string]model.File {
	out := map[string]model.File{}
	for _, name := range names {
		p := filepath.Join(s.dir, name)
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			s.t.Fatal(err)
		}
		info, _ := os.Lstat(p)
		st := info.Sys().(*syscall.Stat_t)
		out[name] = model.File{Path: p, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink)}
	}
	return out
}

// publish tells the inventory which files each torrent has (known), and
// qBittorrent which it has (inClient).
func (s *sharedSetup) publish(files map[string]model.File, known map[string][]string, inClient map[string][]string) {
	var refs []model.TorrentFileRef
	byFile := map[string][]string{}
	for h, names := range known {
		for i, name := range names {
			refs = append(refs, model.TorrentFileRef{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: h, FileIndex: i, Path: files[name].Path})
			byFile[name] = append(byFile[name], h)
		}
	}
	var torrents []model.Torrent
	for h := range inClient {
		shares := map[string]bool{}
		for _, name := range known[h] {
			for _, o := range byFile[name] {
				if o != h {
					shares[o] = true
				}
			}
		}
		var list []string
		for o := range shares {
			list = append(list, o)
		}
		torrents = append(torrents, model.Torrent{Hash: h, Name: "Release " + h[:1], AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-1", Client: "Downloader", SharesFilesWith: list})
	}
	var fileList []model.File
	for _, f := range files {
		fileList = append(fileList, f)
	}
	if err := s.database.PublishReconciliation(1, fileList, nil, refs, nil, torrents, nil); err != nil {
		s.t.Fatal(err)
	}
	s.qb.mu.Lock()
	s.qb.torrents = map[string][]string{}
	for h, names := range inClient {
		s.qb.torrents[h] = names
	}
	s.qb.deletes = nil
	s.qb.mu.Unlock()
}

func (s *sharedSetup) run(form url.Values) (results, errs []string) {
	server := &Server{inv: inventory.New(s.cfg, s.database)}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Overtide-Overlay", "1")
	r.ParseForm()
	w := httptest.NewRecorder()
	server.executeRemovalNowContext(w, r, context.Background())
	var out struct{ Results, Errors []string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		s.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return out.Results, out.Errors
}

func (s *sharedSetup) exists(name string) bool {
	_, err := os.Stat(filepath.Join(s.dir, name))
	return err == nil
}

const (
	hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func removeForm(hash string, others ...string) url.Values {
	form := url.Values{"kind": {"torrent"}, "hash": {hash}, "service_id": {"qbittorrent-1"}, "target": {"1"}}
	for _, o := range others {
		form.Add("torrent", o)
	}
	return form
}

// A torrent with two files, one of which another torrent seeds: it goes
// without its data, Overtide deletes the file only it used, and the shared
// one stays; history counts only what went.
func TestRemovalOfAPartlySharedTorrent(t *testing.T) {
	s := newSharedSetup(t)
	files := s.write("Film.mkv", "Film.nfo")
	both := map[string][]string{hashA: {"Film.mkv", "Film.nfo"}, hashB: {"Film.nfo"}}
	s.publish(files, both, both)
	results, errs := s.run(removeForm(hashA))
	if len(errs) != 0 || len(s.qb.deletes) != 1 || s.qb.deletes[0] != "a deleteFiles=false" {
		t.Fatalf("%v %v %v", results, errs, s.qb.deletes)
	}
	if s.exists("Film.mkv") || !s.exists("Film.nfo") {
		t.Fatalf("its own file kept %v, the shared one gone %v", s.exists("Film.mkv"), !s.exists("Film.nfo"))
	}
	events, err := s.database.HistoryEvents(1)
	if err != nil || len(events) != 1 || events[0].MediaBytes != 4 {
		t.Fatalf("history: %+v %v", events, err)
	}
}

// The plan knows none of the torrent's files (added again since the last
// refresh): qBittorrent's own list of them is checked, and another torrent
// using one keeps it. When qBittorrent can't list them, nothing is removed.
func TestRemovalChecksFilesThePlanDoesntKnow(t *testing.T) {
	s := newSharedSetup(t)
	files := s.write("Film.mkv")
	s.publish(files, map[string][]string{hashB: {"Film.mkv"}}, map[string][]string{hashA: {"Film.mkv"}, hashB: {"Film.mkv"}})
	results, errs := s.run(removeForm(hashA))
	if len(errs) != 0 || len(s.qb.deletes) != 1 || s.qb.deletes[0] != "a deleteFiles=false" || !s.exists("Film.mkv") {
		t.Fatalf("unknown files: %v %v %v, file there %v", results, errs, s.qb.deletes, s.exists("Film.mkv"))
	}

	s.publish(files, map[string][]string{hashB: {"Film.mkv"}}, map[string][]string{hashA: {"Film.mkv"}, hashB: {"Film.mkv"}})
	s.qb.mu.Lock()
	s.qb.noFiles[hashA] = true
	s.qb.mu.Unlock()
	results, errs = s.run(removeForm(hashA))
	if len(errs) == 0 || len(s.qb.deletes) != 0 || !s.exists("Film.mkv") {
		t.Fatalf("no file list: %v %v %v", results, errs, s.qb.deletes)
	}
}

// Two torrents sharing a file, removed together: the first goes without
// it, the last with it; when the last one's removal fails, the file stays
// for it.
func TestRemovalOfAGroupFailingMidway(t *testing.T) {
	s := newSharedSetup(t)
	files := s.write("Film.mkv")
	both := map[string][]string{hashA: {"Film.mkv"}, hashB: {"Film.mkv"}}
	s.publish(files, both, both)
	s.qb.mu.Lock()
	s.qb.refuse[hashB] = true
	s.qb.mu.Unlock()
	results, errs := s.run(removeForm(hashA, hashB))
	if len(errs) == 0 || len(s.qb.deletes) < 2 || s.qb.deletes[0] != "a deleteFiles=false" || !s.exists("Film.mkv") {
		t.Fatalf("%v %v %v, file there %v", results, errs, s.qb.deletes, s.exists("Film.mkv"))
	}
}
