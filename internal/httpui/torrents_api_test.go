package httpui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// torrentsTestServer is a server on a real store holding what's given, with
// the services given, its routes without the sign-in.
func torrentsTestServer(t *testing.T, services []config.Service, torrents []model.Torrent, unmanaged []model.UnmanagedFile) (http.Handler, *store.Store) {
	t.Helper()
	return torrentsTestServerWithFiles(t, services, torrents, unmanaged, nil, nil)
}

// torrentsTestServerWithFiles also holds files and which torrent each
// belongs to: what a torrent frees, or that its files are gone, is worked
// out from them.
func torrentsTestServerWithFiles(t *testing.T, services []config.Service, torrents []model.Torrent, unmanaged []model.UnmanagedFile, files []model.File, refs []model.TorrentFileRef) (http.Handler, *store.Store) {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.PublishReconciliation(1, files, nil, refs, unmanaged, torrents, nil); err != nil {
		t.Fatal(err)
	}
	server, err := New(inventory.New(config.Config{Services: services}, database), nil)
	if err != nil {
		t.Fatal(err)
	}
	return server.routes(), database
}

func getJSON[T any](t *testing.T, handler http.Handler, path string) T {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
	}
	var v T
	if err := json.Unmarshal(response.Body.Bytes(), &v); err != nil {
		t.Fatalf("%s: not JSON: %q", path, response.Body.String())
	}
	return v
}

var qbittorrent = []config.Service{{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: "http://192.0.2.1:8080"}}

// Without a torrent client the page says so, rather than a list where
// nothing matches: nothing produces torrents at all.
func TestTorrentListSaysWhenNoTorrentClient(t *testing.T) {
	handler, _ := torrentsTestServer(t, nil, nil, nil)
	if list := getJSON[torrentListResponse](t, handler, "/api/torrent-list"); list.HasTorrentClient {
		t.Fatalf("no torrent client configured, but: %+v", list)
	}
	handler, _ = torrentsTestServer(t, qbittorrent, []model.Torrent{{Hash: "abc", Name: "Torrent", AssociationStatus: "ASSOCIATED", ServiceID: "qbittorrent-1"}}, nil)
	list := getJSON[torrentListResponse](t, handler, "/api/torrent-list")
	if !list.HasTorrentClient || len(list.Torrents) != 1 || list.Torrents[0].Name != "Torrent" || list.Torrents[0].Status != model.TorrentCurrent {
		t.Fatalf("%+v", list)
	}
}

// A superseded torrent keeps the title it belonged to, to show as
// historical.
func TestTorrentListShowsFormerMedia(t *testing.T) {
	handler, _ := torrentsTestServer(t, qbittorrent, []model.Torrent{{Hash: "old", Name: "Old.Release", AssociationStatus: "SUPERSEDED", ServiceID: "qbittorrent-1",
		FormerMediaItems: []model.MediaRef{{Type: model.Movie, SourceID: 42, Title: "Example", Year: 2025}}}}, nil)
	list := getJSON[torrentListResponse](t, handler, "/api/torrent-list")
	if len(list.Torrents) != 1 || len(list.Torrents[0].FormerMedia) != 1 || list.Torrents[0].FormerMedia[0].Title != "Example" || list.Torrents[0].Media == nil {
		t.Fatalf("%+v", list.Torrents)
	}
}

// Filters, sort and paging come from the address, and the answer says how
// it understood them; the counts are of every torrent, not the filtered.
func TestTorrentListFiltersSortsAndPages(t *testing.T) {
	var torrents []model.Torrent
	var files []model.File
	var refs []model.TorrentFileRef
	for i, name := range []string{"Charlie", "alpha", "Bravo"} {
		hash := strings.Repeat(string(rune('a'+i)), 40)
		torrents = append(torrents, model.Torrent{Hash: hash, Name: name, AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1"})
		// Each has a file of its own, (i+1) KiB, on no other path.
		path := "/data/torrents/" + name + ".mkv"
		files = append(files, model.File{Path: path, SizeBytes: int64(i+1) * 1024, Exists: true, IdentityKnown: true, Device: 1, Inode: uint64(i + 1), Links: 1})
		refs = append(refs, model.TorrentFileRef{ServiceID: "qbittorrent-1", Hash: hash, Path: path})
	}
	torrents = append(torrents, model.Torrent{Hash: strings.Repeat("d", 40), Name: "Delta", AssociationStatus: model.TorrentCurrent, ServiceID: "qbittorrent-1"})
	handler, _ := torrentsTestServerWithFiles(t, qbittorrent, torrents, nil, files, refs)

	list := getJSON[torrentListResponse](t, handler, "/api/torrent-list?status=superseded&sort=name&order=asc&page_size=25&page=1")
	if list.AllItems != 4 || list.TotalItems != 3 || list.Superseded != 3 || list.Current != 1 || list.Status != "superseded" {
		t.Fatalf("counts: %+v", list)
	}
	if list.ObsoleteKnown != 3 || list.ObsoleteReclaimable != "6.0 KiB" {
		t.Fatalf("obsolete: %d %q", list.ObsoleteKnown, list.ObsoleteReclaimable)
	}
	var names []string
	for _, row := range list.Torrents {
		names = append(names, row.Name)
	}
	if strings.Join(names, ",") != "alpha,Bravo,Charlie" || list.Paging != (pageInfo{Page: 1, PageSize: 25, TotalPages: 1, Sort: "name", Order: "asc"}) {
		t.Fatalf("sorted: %v %+v", names, list.Paging)
	}
	// Unknown values fall back to the defaults; a page past the end is the last.
	list = getJSON[torrentListResponse](t, handler, "/api/torrent-list?sort=nonsense&order=sideways&page_size=7&page=9&status=whatever&reclaimable=x&activity=y")
	if list.Paging != (pageInfo{Page: 1, PageSize: 50, TotalPages: 1, Sort: "status", Order: "asc"}) || list.Status != "any" || list.Reclaimable != "any" || list.Activity != "any" {
		t.Fatalf("defaults: %+v %q %q %q", list.Paging, list.Status, list.Reclaimable, list.Activity)
	}
	list = getJSON[torrentListResponse](t, handler, "/api/torrent-list?q=bravo")
	if len(list.Torrents) != 1 || list.Torrents[0].Name != "Bravo" || list.Query != "bravo" {
		t.Fatalf("search: %+v", list.Torrents)
	}
}

// The detail asks qBittorrent for what it alone knows; when it doesn't
// answer, the page still shows what the inventory has, and says some
// details are missing.
func TestTorrentDetail(t *testing.T) {
	hash := strings.Repeat("a", 40)
	// A port nothing listens on: refused at once.
	refused := []config.Service{{ID: "qbittorrent-1", Type: "qbittorrent", Name: "Downloader", URL: "http://127.0.0.1:1"}}
	handler, _ := torrentsTestServer(t, refused, []model.Torrent{{Hash: hash, Name: "Release", AssociationStatus: model.TorrentCurrent, ServiceID: "qbittorrent-1",
		Protected: true, ProtectionReason: "kept", MediaItems: []model.MediaRef{{Type: model.Movie, SourceID: 7, Title: "Film", Year: 2020, ServiceID: "radarr-1"}}}}, nil)
	detail := getJSON[torrentDetailResponse](t, handler, "/api/torrents/"+hash+"?service_id=qbittorrent-1")
	if detail.Operation != nil || detail.Torrent == nil || detail.Torrent.Name != "Release" || !detail.Torrent.Protected || len(detail.Torrent.Media) != 1 || detail.Files == nil || !detail.DetailUnavailable {
		t.Fatalf("%+v %+v", detail, detail.Torrent)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/torrents/"+strings.Repeat("f", 40)+"?service_id=qbittorrent-1", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"error"`) {
		t.Fatalf("unknown torrent: %d %s", response.Code, response.Body.String())
	}
}

// Removing the torrents whose files are gone is a JSON write: a form is
// refused before anything is asked of qBittorrent.
func TestFilesMissing(t *testing.T) {
	// "one"'s only file is gone from disk; "two"'s is there.
	handler, _ := torrentsTestServerWithFiles(t, qbittorrent, []model.Torrent{
		{Hash: strings.Repeat("a", 40), Name: "one", AssociationStatus: model.TorrentOrphaned, ServiceID: "qbittorrent-1"},
		{Hash: strings.Repeat("b", 40), Name: "two", AssociationStatus: model.TorrentCurrent, ServiceID: "qbittorrent-1"},
	}, nil, []model.File{
		{Path: "/data/torrents/one.mkv", SizeBytes: 10, Exists: false},
		{Path: "/data/torrents/two.mkv", SizeBytes: 10, Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 1},
	}, []model.TorrentFileRef{
		{ServiceID: "qbittorrent-1", Hash: strings.Repeat("a", 40), Path: "/data/torrents/one.mkv"},
		{ServiceID: "qbittorrent-1", Hash: strings.Repeat("b", 40), Path: "/data/torrents/two.mkv"},
	})
	missing := getJSON[struct {
		Torrents []missingTorrent `json:"torrents"`
		DryRun   bool             `json:"dryRun"`
	}](t, handler, "/api/torrents-missing")
	if len(missing.Torrents) != 1 || missing.Torrents[0].Name != "one" {
		t.Fatalf("%+v", missing)
	}
	if list := getJSON[torrentListResponse](t, handler, "/api/torrent-list"); list.FilesMissing != 1 {
		t.Fatalf("list's files missing: %d", list.FilesMissing)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/torrents-missing", nil)
	request.Method = http.MethodDelete
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("DELETE is not how they're removed")
	}
}

func unmanagedFiles(t *testing.T, handler http.Handler, query string) unmanagedResponse {
	t.Helper()
	return getJSON[unmanagedResponse](t, handler, "/api/unmanaged-files"+query)
}

func unmanagedPaths(list unmanagedResponse) string {
	var paths []string
	for _, row := range list.Files {
		for _, p := range row.Paths {
			paths = append(paths, p.Path)
		}
	}
	return strings.Join(paths, ",")
}

// A file hardlinked under several paths is one row carrying every path, so
// removing it takes them all (the removal window is given every one).
func TestUnmanagedRowCarriesEveryHardlinkedPath(t *testing.T) {
	handler, _ := torrentsTestServer(t, nil, nil, []model.UnmanagedFile{
		{Path: "/data/a/file.mkv", SizeBytes: 54321, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
		{Path: "/data/b/file.mkv", SizeBytes: 54321, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
	})
	list := unmanagedFiles(t, handler, "")
	if len(list.Files) != 1 || len(list.Files[0].Paths) != 2 || unmanagedPaths(list) != "/data/a/file.mkv,/data/b/file.mkv" {
		t.Fatalf("%+v", list.Files)
	}
	if list.Files[0].Reclaimable != "53.0 KiB" || list.Files[0].MissingLinks != 0 || list.Reclaimable == "" {
		t.Fatalf("%+v %q", list.Files[0], list.Reclaimable)
	}
}

func TestUnmanagedStatusFilterNarrowsToReclaimableOrSharedOrUnknownJSON(t *testing.T) {
	handler, _ := torrentsTestServer(t, nil, nil, []model.UnmanagedFile{
		{Path: "/mnt/media/reclaimable-one.mkv", SizeBytes: 100, Device: 1, Inode: 1, Links: 1, ReclaimableKnown: true},
		// Links=2 but only one path is known to this scan: still shared.
		{Path: "/mnt/media/shared-one.mkv", SizeBytes: 100, Device: 2, Inode: 2, Links: 2, ReclaimableKnown: true},
		{Path: "/mnt/media/unknown-one.mkv", SizeBytes: 100, Device: 3, Inode: 3, Links: 1, ReclaimableKnown: false},
	})
	for status, want := range map[string]string{"reclaimable": "/mnt/media/reclaimable-one.mkv", "shared": "/mnt/media/shared-one.mkv", "unknown": "/mnt/media/unknown-one.mkv"} {
		list := unmanagedFiles(t, handler, "?status="+status)
		if got := unmanagedPaths(list); got != want || list.Status != status || list.AllItems != 3 || list.TotalItems != 1 {
			t.Fatalf("status=%s: %q (%+v)", status, got, list)
		}
	}
	if shared := unmanagedFiles(t, handler, "?status=shared"); shared.Files[0].MissingLinks != 1 {
		t.Fatalf("a hardlink outside the known paths: %+v", shared.Files[0])
	}
}

func TestUnmanagedRootFilterNarrowsToSelectedRootJSON(t *testing.T) {
	handler, _ := torrentsTestServer(t, nil, nil, []model.UnmanagedFile{
		{Path: "/mnt/movies/leftover.mkv", SizeBytes: 100, Device: 1, Inode: 1, Links: 1, ReclaimableKnown: true,
			StorageContexts: []model.StorageContext{{ServiceName: "Radarr", Root: "/mnt/movies", RootLabel: "movies"}}},
		{Path: "/mnt/series/leftover.mkv", SizeBytes: 100, Device: 2, Inode: 2, Links: 1, ReclaimableKnown: true,
			StorageContexts: []model.StorageContext{{ServiceName: "Sonarr", Root: "/mnt/series", RootLabel: "series"}}},
	})
	list := unmanagedFiles(t, handler, "?root="+url.QueryEscape("Radarr|/mnt/movies"))
	if unmanagedPaths(list) != "/mnt/movies/leftover.mkv" || list.Root != "Radarr|/mnt/movies" || len(list.RootOptions) != 2 || list.RootOptions[0].Label != "Radarr · movies" {
		t.Fatalf("%+v", list)
	}
	// A root that isn't one of them is no filter.
	if list := unmanagedFiles(t, handler, "?root=nowhere"); list.Root != "" || list.TotalItems != 2 {
		t.Fatalf("unknown root: %+v", list)
	}
}

func TestUnmanagedMinSizeFilterExcludesSmallerFilesJSON(t *testing.T) {
	const mib = 1024 * 1024
	handler, _ := torrentsTestServer(t, nil, nil, []model.UnmanagedFile{
		{Path: "/mnt/media/small.mkv", SizeBytes: 1 * mib, Device: 1, Inode: 1, Links: 1, ReclaimableKnown: true},
		{Path: "/mnt/media/large.mkv", SizeBytes: 500 * mib, Device: 2, Inode: 2, Links: 1, ReclaimableKnown: true},
	})
	if list := unmanagedFiles(t, handler, "?min_size_mib=100"); unmanagedPaths(list) != "/mnt/media/large.mkv" || list.MinSizeMiB != "100" {
		t.Fatalf("%+v", list)
	}
	// Sorted by physical potential, largest first, by default.
	if list := unmanagedFiles(t, handler, ""); unmanagedPaths(list) != "/mnt/media/large.mkv,/mnt/media/small.mkv" || list.Paging.Sort != "reclaimable" || list.Paging.Order != "desc" {
		t.Fatalf("default sort: %q %+v", unmanagedPaths(list), list.Paging)
	}
}

// While a removal of one of a file's paths is pending, the listing doesn't
// show the file with a false "hardlink outside known topology": the whole
// physical file waits for the removal.
func TestUnmanagedListingDoesNotDesyncHardlinkCountWhenOnePathIsPendingJSON(t *testing.T) {
	pathA := "/mnt/media/Series/show/episode.mkv"
	pathB := "/mnt/media/downloads/complete/show/episode.mkv"
	handler, database := torrentsTestServer(t, nil, nil, []model.UnmanagedFile{
		{Path: pathA, SizeBytes: 100, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
		{Path: pathB, SizeBytes: 100, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
	})
	queuedPayload, err := json.Marshal(queuedRemovalPayload{Command: scheduledRemovalCommand{Form: url.Values{"kind": {"unmanaged"}, "path": {pathA}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "started", RequestedKind: "unmanaged", Payload: queuedPayload}); err != nil {
		t.Fatal(err)
	}
	for _, row := range unmanagedFiles(t, handler, "").Files {
		if row.MissingLinks > 0 {
			t.Fatalf("a false missing hardlink while a sibling path removal was pending: %+v", row)
		}
	}
}
