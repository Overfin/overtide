package httpui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
	"github.com/overfin/overtide/internal/tasks"
)

// libraryServer is a server on the test inventory (one film, two
// torrents), without the sign-in.
func libraryServer(t *testing.T, cfg config.Config) (*Server, http.Handler) {
	t.Helper()
	server, err := New(newTestInventoryWithConfig(t, cfg), nil)
	if err != nil {
		t.Fatal(err)
	}
	return server, server.routes()
}

func libraryGet[T any](t *testing.T, handler http.Handler, path string) (T, int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	var v T
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &v); err != nil {
			t.Fatalf("%s: not JSON: %q", path, response.Body.String())
		}
	}
	return v, response.Code
}

// With neither a Radarr nor a Sonarr, there's nothing to manage: the page
// shows why instead of an empty, filterable table.
func TestLibraryAPISaysWhenNoMediaLibrary(t *testing.T) {
	_, handler := libraryServer(t, config.Config{})
	list, code := libraryGet[libraryJSON](t, handler, "/api/library")
	if code != http.StatusOK || list.HasMediaLibrary || list.HasTorrentClient {
		t.Fatalf("code=%d %+v", code, list)
	}
}

// The filters offered follow what the library holds: the Type filter only
// with more than one type, Source only with more than one source, Torrent
// only with a torrent client (otherwise it could never match anything).
func TestLibraryAPIOffersOnlyUsefulFilters(t *testing.T) {
	cfg := config.Config{Services: []config.Service{{Type: "radarr", Name: "Movies"}}}
	_, handler := libraryServer(t, cfg)
	list, _ := libraryGet[libraryJSON](t, handler, "/api/library")
	if !list.HasMediaLibrary || list.HasTorrentClient {
		t.Fatalf("services: %+v", list)
	}
	if len(list.TypeOptions) != 1 || list.TypeOptions[0] != (libraryOption{Value: "movie", Label: "Movies"}) || len(list.SourceOptions) != 0 {
		t.Fatalf("one type, one source: types=%v sources=%v", list.TypeOptions, list.SourceOptions)
	}
	if list.Torrent != "any" {
		t.Fatalf("no torrent client, no torrent filter: %q", list.Torrent)
	}

	cfg.Services = append(cfg.Services, config.Service{Type: "qbittorrent", Name: "Downloader"})
	_, handler = libraryServer(t, cfg)
	if list, _ = libraryGet[libraryJSON](t, handler, "/api/library?torrent=no&show_no_files=1"); !list.HasTorrentClient || list.Torrent != "no" || !list.ShowNoFiles {
		t.Fatalf("with a torrent client: %+v", list)
	}
}

// The list is the films and series, filtered, sorted and paged as asked;
// the test film has no files, so it shows only when asked.
func TestLibraryAPIListsAndFilters(t *testing.T) {
	_, handler := libraryServer(t, config.Config{Services: []config.Service{{Type: "radarr", Name: "Movies"}}})
	list, _ := libraryGet[libraryJSON](t, handler, "/api/library?show_no_files=1&page_size=25&sort=value&order=desc")
	if list.All != 1 || list.Total != 1 || len(list.Rows) != 1 || list.PageSize != 25 || list.Sort != "value" || list.Order != "desc" {
		t.Fatalf("%+v", list)
	}
	row := list.Rows[0]
	if row.Title != "Test Movie" || row.Type != "movie" || row.SourceID != 1 || row.ServiceID != "radarr-1" || row.Size != "0.0 B" {
		t.Fatalf("row: %+v", row)
	}
	if list, _ = libraryGet[libraryJSON](t, handler, "/api/library"); list.Total != 0 || len(list.Rows) != 0 || list.Rows == nil {
		t.Fatalf("no-file titles hidden by default: %+v", list)
	}
	if list, _ = libraryGet[libraryJSON](t, handler, "/api/library?show_no_files=1&q=nothing"); list.Total != 0 || list.All != 1 {
		t.Fatalf("search: %+v", list)
	}
}

// A title's page: the title, its value, its keep key; an unknown one is
// 404, as JSON.
func TestLibraryTitleAPI(t *testing.T) {
	_, handler := libraryServer(t, config.Config{})
	detail, code := libraryGet[mediaDetailJSON](t, handler, "/api/library/movie/1?service_id=radarr-1")
	if code != http.StatusOK || detail.Media.Title != "Test Movie" || detail.Media.ServiceID != "radarr-1" || detail.KeptKey != config.KeptMediaKey("movie", "radarr-1", 1) || detail.KeepName != "Test Movie" {
		t.Fatalf("code=%d %+v", code, detail)
	}
	if detail.Current == nil || detail.Superseded == nil || detail.Files == nil || detail.Media.Reasons == nil {
		t.Fatalf("lists must be [] rather than null: %+v", detail)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/library/movie/99", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"error"`) {
		t.Fatalf("unknown title: %d %q", response.Code, response.Body.String())
	}
}

// A file's other paths are shown when it's hardlinked, with who owns each;
// sizes are written as the pages always wrote them.
func TestFileRowsShowHardlinks(t *testing.T) {
	rows := fileRows([]inventory.FileView{{
		File:       model.File{Path: "/media/a.mkv", SizeBytes: 100, Exists: true, IdentityKnown: true, Links: 2},
		SharedWith: []inventory.FilePeer{{Path: "/downloads/a.mkv", Torrents: []inventory.TorrentFileOwner{{Hash: "abc", Name: "Release"}}}},
	}, {
		File:       model.File{Path: "/media/b.mkv", SizeBytes: 100, Exists: true, IdentityKnown: true, Links: 1},
		SharedWith: []inventory.FilePeer{{Path: "/elsewhere/b.mkv"}},
	}})
	if len(rows) != 2 || rows[0].Links != 2 || len(rows[0].SharedWith) != 1 || rows[0].SharedWith[0].Path != "/downloads/a.mkv" || rows[0].Size != "100.0 B" {
		t.Fatalf("hardlinked file: %+v", rows)
	}
	if len(rows[1].SharedWith) != 0 {
		t.Fatalf("a file with one link shows no other path: %+v", rows[1])
	}
	if humanSize(0) != "0.0 B" || humanSize(-5) != "0.0 B" {
		t.Fatal("sizes")
	}
}

// The title page's torrents are grouped as the page shows them, and only
// the non-current ones say what removing them would free.
func TestTorrentRowsReclaimable(t *testing.T) {
	list := []model.Torrent{{Hash: "a", Name: "A", SizeBytes: 2048, Ratio: 1.5, SeedsSwarm: 3, LeechersSwarm: 1, ReclaimableKnown: true, ReclaimableBytes: 1024}}
	if rows := torrentRows(list, false); rows[0].Reclaimable != "" || rows[0].Size != "2.0 KiB" || rows[0].Seeds != 3 {
		t.Fatalf("current: %+v", rows)
	}
	if rows := torrentRows(list, true); rows[0].Reclaimable != "1.0 KiB" {
		t.Fatalf("superseded: %+v", rows)
	}
}

// Keeping saves the keep, and taking it off forgets it; a title's keep is
// a tag in its Radarr or Sonarr, so without one the service's error is
// what the page shows.
func TestLibraryKeepAPI(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	session := setUp(t, handler)
	key := config.KeptTorrentKey(strings.Repeat("a", 40))
	if response := post(t, handler, "/api/storage/keep", keepRequest{Key: key, Name: "Release"}, session); response.Code != http.StatusOK {
		t.Fatalf("keep: %d %q", response.Code, response.Body.String())
	}
	if response := post(t, handler, "/api/storage/keep", keepRequest{Key: key, Unkeep: true}, session); response.Code != http.StatusOK {
		t.Fatalf("unkeep: %d %q", response.Code, response.Body.String())
	}
	if response := post(t, handler, "/api/storage/keep", keepRequest{}, session); response.Code != http.StatusBadRequest {
		t.Fatalf("no key: %d", response.Code)
	}
	title := post(t, handler, "/api/storage/keep", keepRequest{Key: config.KeptMediaKey("movie", "radarr-1", 7)}, session)
	if title.Code != http.StatusInternalServerError || !strings.Contains(title.Body.String(), "radarr-1") {
		t.Fatalf("title without its service: %d %q", title.Code, title.Body.String())
	}
}

// History lists additions, one row per title and day, and live removals,
// the newest first; a dry run is left out, though its operation still
// shows on its own with what it did.
func TestHistoryAPI(t *testing.T) {
	server, handler := libraryServer(t, config.Config{})
	db := server.inv.Store()
	payload := json.RawMessage(`{"plan":{"files":[{"path":"/movies/a.mkv","owner":"radarr","selected":true}]},"results":["deleted"],"errors":["one failed"]}`)
	now := time.Now()
	dry, err := db.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "done", DryRun: true, RequestedKind: "media", RequestedLabel: "Dry Movie", Payload: payload, CreatedAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "partial", DryRun: false, RequestedKind: "media", RequestedLabel: "Test Movie", ReclaimableBytes: 2048, Payload: payload, CreatedAt: now.Add(-2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var files []store.ImportedFile
	for episode := 1; episode <= 3; episode++ {
		files = append(files, store.ImportedFile{ServiceID: "sonarr-1", ServiceName: "Sonarr", RecordID: episode, Kind: "series", SourceID: 9, Title: "A Show", Season: 1, Episode: episode,
			Size: 1024, Origin: "wish", ImportedAt: now.Add(-time.Duration(episode) * time.Second)})
	}
	files = append(files, store.ImportedFile{ServiceID: "sonarr-1", RecordID: 9, Kind: "series", SourceID: 9, Title: "A Show", Season: 1, Episode: 5, ImportedAt: now.AddDate(0, 0, -20)})
	if err := db.AddImportedFiles(append(files, files[0])); err != nil {
		t.Fatal(err)
	}

	history, code := libraryGet[activityPageJSON](t, handler, "/api/history")
	if code != http.StatusOK || history.Total != 2 || len(history.Rows) != 2 {
		t.Fatalf("code=%d %+v", code, history)
	}
	added, removed := history.Rows[0], history.Rows[1]
	if added.Action != "added" || added.Title != "A Show" || added.Files != 3 || added.Episodes != "S1 E1–E3" || added.Size != "3.0 KiB" || added.Origin != "wish" || added.Day == "" {
		t.Fatalf("added: %+v", added)
	}
	if removed.Action != "removed" || removed.Title != "Test Movie" || removed.Status != "partial" || removed.Size != "2.0 KiB" || removed.Operation == 0 {
		t.Fatalf("removed: %+v", removed)
	}
	if all, _ := libraryGet[activityPageJSON](t, handler, "/api/history?days=0"); all.Total != 3 {
		t.Fatalf("every day: %+v", all)
	}
	if failed, _ := libraryGet[activityPageJSON](t, handler, "/api/history?show=failed&q=test"); failed.Total != 1 || failed.Rows[0].Title != "Test Movie" {
		t.Fatalf("failed: %+v", failed)
	}
	if none, _ := libraryGet[activityPageJSON](t, handler, "/api/history?q=50%25"); none.Total != 0 {
		t.Fatalf("a %% searched for: %+v", none)
	}
	if _, code := libraryGet[activityPageJSON](t, handler, "/api/history?show=everything"); code != http.StatusBadRequest {
		t.Fatalf("unknown show: %d", code)
	}

	day, _ := libraryGet[struct {
		Files []importedFileJSON `json:"files"`
	}](t, handler, "/api/history/added?service=sonarr-1&kind=series&id=9&day="+added.Day)
	if len(day.Files) != 3 || day.Files[0].Episode != "S01E01" {
		t.Fatalf("the day's files: %+v", day)
	}
	operation, code := libraryGet[historyEventJSON](t, handler, "/api/history/operations/"+strconv.FormatInt(dry, 10))
	if code != http.StatusOK || operation.Label != "Dry Movie" || !operation.DryRun || len(operation.Files) != 1 || !operation.Files[0].Selected || len(operation.Results) != 1 || len(operation.Errors) != 1 {
		t.Fatalf("operation: %d %+v", code, operation)
	}
}

// Episodes are written by season, consecutive ones as a run.
func TestEpisodeRanges(t *testing.T) {
	got := episodeRanges([][2]int{{2, 1}, {1, 5}, {1, 1}, {1, 2}, {1, 3}, {1, 3}})
	if got != "S1 E1–E3, E5 · S2 E1" {
		t.Fatalf("%q", got)
	}
	if episodeRanges(nil) != "" {
		t.Fatal("no episodes")
	}
}

// A task's row says its schedule and its last run's duration the way the
// page always did, and only a scheduled task has a next run.
func TestTaskRow(t *testing.T) {
	next := time.Now().Add(time.Hour)
	row := taskRow(tasks.Status{ID: "inventory", Name: "Inventory refresh", Interval: time.Hour, NextRun: next, CanRun: true, Queued: true})
	if row.Schedule != "Every "+humanDuration(3600) || row.Duration != "—" || !row.Scheduled || !row.NextRun.Equal(next) || !row.Busy {
		t.Fatalf("%+v", row)
	}
	row = taskRow(tasks.Status{ID: "removal", LastFinished: time.Now(), LastDuration: 90 * time.Second})
	if row.Schedule != "On demand" || row.Scheduled || !row.NextRun.IsZero() || row.Duration != humanDuration(90) {
		t.Fatalf("%+v", row)
	}
}

// Without a task manager, the Tasks API says so instead of failing.
func TestTasksAPIWithoutManager(t *testing.T) {
	_, handler := libraryServer(t, config.Config{})
	if _, code := libraryGet[tasksJSON](t, handler, "/api/tasks"); code != http.StatusServiceUnavailable {
		t.Fatalf("list: %d", code)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/tasks/run", strings.NewReader(`{"id":"inventory"}`))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("run: %d", response.Code)
	}
}
