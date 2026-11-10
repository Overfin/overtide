package httpui

import (
	"context"
	"encoding/json"
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
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// Two torrents seeding the very same file: removing one keeps the file
// (qBittorrent removes it without its data) and lists the other, unticked;
// ticking both removes the file with them. A torrent qBittorrent says uses
// the file now, though the plan didn't know it, keeps it too.
func TestRemovalKeepsFilesAnotherTorrentUses(t *testing.T) {
	const a, b, c = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccccccccccccccccccc"
	dir := t.TempDir()
	path := filepath.Join(dir, "Release.mkv")
	var mu sync.Mutex
	var deletes []string
	live := []string{a, b} // the torrents qBittorrent has now
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s"})
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/delete":
			mu.Lock()
			deletes = append(deletes, r.Form.Get("hashes")[:1]+" deleteFiles="+r.Form.Get("deleteFiles"))
			var left []string
			for _, h := range live {
				if h != r.Form.Get("hashes") {
					left = append(left, h)
				}
			}
			live = left
			mu.Unlock()
		case "/api/v2/torrents/info":
			mu.Lock()
			var xs []string
			for _, h := range live {
				if only := r.Form.Get("hashes"); only != "" && only != h {
					continue
				}
				xs = append(xs, fmt.Sprintf(`{"hash":%q,"name":"Release.mkv","save_path":%q,"content_path":%q,"progress":1}`, h, dir, path))
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[" + strings.Join(xs, ",") + "]"))
		case "/api/v2/torrents/files":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"index":0,"name":"Release.mkv","size":4,"progress":1}]`))
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		}
	}))
	defer fake.Close()

	reset := func() model.File {
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Lstat(path)
		st := info.Sys().(*syscall.Stat_t)
		return model.File{Path: path, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: uint64(st.Nlink)}
	}
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := config.Config{Removal: config.RemovalConfig{DryRun: false}, Services: []config.Service{
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: fake.URL, AllowAutomaticRemoval: true},
	}}
	publish := func(known ...string) {
		file := reset()
		var refs []model.TorrentFileRef
		var torrents []model.Torrent
		for _, h := range known {
			refs = append(refs, model.TorrentFileRef{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: h, Path: path})
			var others []string
			for _, o := range known {
				if o != h {
					others = append(others, o)
				}
			}
			torrents = append(torrents, model.Torrent{Hash: h, Name: "Release " + h[:1], AssociationStatus: model.TorrentUnassociated, ServiceID: "qbittorrent-1", Client: "Downloader", SharesFilesWith: others})
		}
		if err := database.PublishReconciliation(1, []model.File{file}, nil, refs, nil, torrents, nil); err != nil {
			t.Fatal(err)
		}
	}
	run := func(form url.Values, inClient ...string) (results, errs []string) {
		mu.Lock()
		deletes = nil
		live = inClient
		mu.Unlock()
		server := &Server{inv: inventory.New(cfg, database)}
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Overtide-Overlay", "1")
		r.ParseForm()
		w := httptest.NewRecorder()
		server.executeRemovalNowContext(w, r, context.Background())
		var out struct{ Results, Errors []string }
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return out.Results, out.Errors
	}

	// The page: the other torrent is listed, unticked, and nothing is freed.
	publish(a, b)
	server := &Server{inv: inventory.New(cfg, database)}
	plan, err := server.buildTorrentRemovalPlan(a, "qbittorrent-1", true, map[string]bool{}, map[string]bool{}, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Related) != 1 || !strings.EqualFold(plan.Related[0].Torrent.Hash, b) || plan.Related[0].Selected || !plan.Related[0].SharesFiles || plan.Plan.SelectedPathBytes != 0 || !plan.Plan.Kept(path) {
		t.Fatalf("one ticked: related %+v, freed %d", plan.Related, plan.Plan.SelectedPathBytes)
	}
	both, err := server.buildTorrentRemovalPlan(a, "qbittorrent-1", true, map[string]bool{}, map[string]bool{}, map[string]bool{b: true})
	if err != nil || both.Plan.SelectedPathBytes != 4 || both.Plan.Kept(path) {
		t.Fatalf("both ticked: freed %d, %v", both.Plan.SelectedPathBytes, err)
	}
	if _, err := server.buildTorrentRemovalPlan(a, "qbittorrent-1", true, map[string]bool{}, map[string]bool{}, map[string]bool{c: true}); err == nil {
		t.Fatal("a torrent outside the group was accepted")
	}

	// Removing one: without its data, and the file stays.
	results, errs := run(url.Values{"kind": {"torrent"}, "hash": {a}, "service_id": {"qbittorrent-1"}, "target": {"1"}}, a, b)
	if len(errs) != 0 || len(deletes) != 1 || deletes[0] != "a deleteFiles=false" {
		t.Fatalf("one: %v %v %v", results, errs, deletes)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the shared file went: %v", err)
	}

	// Both: the first without the file the second still uses, the last
	// one with it.
	publish(a, b)
	results, errs = run(url.Values{"kind": {"torrent"}, "hash": {a}, "service_id": {"qbittorrent-1"}, "target": {"1"}, "torrent": {b}}, a, b)
	if len(errs) != 0 || len(deletes) != 2 || deletes[0] != "a deleteFiles=false" || deletes[1] != "b deleteFiles=true" {
		t.Fatalf("both: %v %v %v", results, errs, deletes)
	}

	// The plan knows only a, but qBittorrent has b on the file now.
	publish(a)
	results, errs = run(url.Values{"kind": {"torrent"}, "hash": {a}, "service_id": {"qbittorrent-1"}, "target": {"1"}}, a, b)
	if len(errs) != 0 || len(deletes) != 1 || deletes[0] != "a deleteFiles=false" {
		t.Fatalf("new cross-seed: %v %v %v", results, errs, deletes)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the file a new torrent uses went: %v", err)
	}

	// Alone for real: with its data.
	publish(a)
	results, errs = run(url.Values{"kind": {"torrent"}, "hash": {a}, "service_id": {"qbittorrent-1"}, "target": {"1"}}, a)
	if len(errs) != 0 || len(deletes) != 1 || deletes[0] != "a deleteFiles=true" {
		t.Fatalf("alone: %v %v %v", results, errs, deletes)
	}
}

// Automatic removal of torrents sharing files: the whole group, every one
// past its obligations, and only the group it was planned with.
func TestAutomaticGroupWhole(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	old := time.Now().Add(-90 * 24 * time.Hour).Unix()
	tor := func(h string, obligation string, shares ...string) model.Torrent {
		t := model.Torrent{Hash: h, Name: "T" + h, ServiceID: "qbittorrent-1", Client: "Downloader", AssociationStatus: model.TorrentUnassociated, SharesFilesWith: shares, AddedOn: old, SeedingTime: 90 * 86400, Ratio: 5}
		if obligation != "" { // a private tracker's seeding time, not met
			t.Private, t.SeedingTime = true, 3*86400
		}
		return t
	}
	torrents := []model.Torrent{
		tor("a", "", "b"), tor("b", "", "a"),
		tor("c", "Private tracker: seeding 3 of 10 days", "d"), tor("d", "", "c"),
		tor("e", "", "f", "g"), tor("f", "", "e", "g"), tor("g", "", "e", "f"),
	}
	// Each group's torrents seed one file together.
	dir := t.TempDir()
	var files []model.File
	var refs []model.TorrentFileRef
	for _, group := range [][]string{{"a", "b"}, {"c", "d"}, {"e", "f", "g"}} {
		p := filepath.Join(dir, group[0]+".mkv")
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Lstat(p)
		st := info.Sys().(*syscall.Stat_t)
		files = append(files, model.File{Path: p, SizeBytes: 4, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, Links: 1})
		for _, h := range group {
			refs = append(refs, model.TorrentFileRef{ServiceID: "qbittorrent-1", Client: "Downloader", Hash: h, Path: p})
		}
	}
	if err := database.PublishReconciliation(1, files, nil, refs, nil, torrents, nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Services: []config.Service{
		{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", AllowAutomaticRemoval: true},
	}}
	cfg.Protection.PrivateMinSeedingDays = 10
	server := &Server{inv: inventory.New(cfg, database)}
	cases := []struct {
		name string
		form url.Values
		ok   bool
	}{
		{"the whole group", url.Values{"kind": {"torrent"}, "hash": {"a"}, "torrent": {"b"}}, true},
		{"one of the group still owes seeding", url.Values{"kind": {"torrent"}, "hash": {"d"}, "torrent": {"c"}}, false},
		{"a torrent joined the group since the plan", url.Values{"kind": {"torrent"}, "hash": {"e"}, "torrent": {"f"}}, false},
	}
	for _, tc := range cases {
		if err := server.cleanupStillEligible(tc.form); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok = %v", tc.name, err, tc.ok)
		}
	}
}
