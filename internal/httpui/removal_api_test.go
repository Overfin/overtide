package httpui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/removal"
)

// The window's JSON request becomes exactly the fields the form had: the
// scheduler, which stores and replays the form, sees what it always saw.
func TestRemovalRequestKeepsTheFormFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/removal/execute", strings.NewReader(`{"kind":"media","operationToken":"tok","mediaType":"movie","mediaId":42,"serviceId":"radarr-1","managedFiles":["radarr:9"],"torrents":["abc"],"excludeMovies":true}`))
	request.Header.Set("Content-Type", "application/json")
	var in removalRequest
	if err := readJSON(request, &in); err != nil {
		t.Fatal(err)
	}
	form := in.form()
	want := url.Values{"kind": {"media"}, "operation_token": {"tok"}, "media_type": {"movie"}, "media_id": {"42"}, "service_id": {"radarr-1"}, "managed_file": {"radarr:9"}, "torrent": {"abc"}, "exclude_movies": {"1"}}
	if form.Encode() != want.Encode() {
		t.Fatalf("form = %v, want %v", form, want)
	}
	// What isn't given isn't there: a field's mere presence is what the
	// scope checks refuse.
	for _, name := range []string{"target", "path", "unmanaged_path", "hash", "exclude_series"} {
		if _, ok := form[name]; ok {
			t.Errorf("%s is in the form though it wasn't given", name)
		}
	}
}

// A form (what another site's page could post) is refused before anything
// is admitted or recorded; so is a field the request doesn't have.
func TestRemovalExecuteRefusesAForm(t *testing.T) {
	fx := newRemovalFixture(t)
	form := url.Values{"kind": {"media"}, "media_type": {"movie"}, "media_id": {"1"}, "service_id": {"radarr-1"}, "managed_file": {"radarr:10"}}
	if response := postForm(t, fx.handler, "/api/removal/execute", form, fx.session); response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a form: %d %s", response.Code, response.Body)
	}
	unknown := send(t, fx.handler, http.MethodPost, "/api/removal/execute", map[string]any{"kind": "media", "mediaType": "movie", "mediaId": 1, "managedFiles": []string{"radarr:10"}, "deleteEverything": true}, fx.session)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field: %d %s", unknown.Code, unknown.Body)
	}
	if events, _ := fx.database.HistoryEvents(10); len(events) != 0 {
		t.Fatalf("a refused request was recorded: %+v", events)
	}
}

// Cross-owner and injected paths are refused through the JSON API exactly
// as through the form: by the scope checks, before admission, with nothing
// recorded or scheduled.
func TestRemovalExecuteRejectsCrossOwnerAndInjectedPaths(t *testing.T) {
	fx := newRemovalFixture(t)
	for name, request := range map[string]removalRequest{
		"torrent with injected library path": {Kind: "torrent", Hash: scheduledHash, ServiceID: "qbittorrent-1", Target: true, UnmanagedPaths: []string{fx.movie.Path}},
		"torrent with media owner":           {Kind: "torrent", Hash: scheduledHash, ServiceID: "qbittorrent-1", Target: true, ManagedFiles: []string{"radarr:10"}},
		"torrent without its target":         {Kind: "torrent", Hash: scheduledHash, ServiceID: "qbittorrent-1"},
		"media with direct path":             {Kind: "media", MediaType: "movie", MediaID: 1, ServiceID: "radarr-1", ManagedFiles: []string{"radarr:10"}, UnmanagedPaths: []string{fx.movie.Path}},
		"media with a path":                  {Kind: "media", MediaType: "movie", MediaID: 1, ServiceID: "radarr-1", ManagedFiles: []string{"radarr:10"}, Paths: []string{fx.movie.Path}},
		"unmanaged with torrent":             {Kind: "unmanaged", Paths: []string{fx.movie.Path}, Torrents: []string{scheduledHash}},
		"unmanaged without a path":           {Kind: "unmanaged"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateRemovalScope(request.form()); err == nil {
				t.Fatalf("scope accepted %+v", request)
			}
			response := post(t, fx.handler, "/api/removal/execute", request, fx.session)
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "no longer valid") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
		})
	}
	if events, _ := fx.database.HistoryEvents(10); len(events) != 0 {
		t.Fatalf("a refused request was recorded: %+v", events)
	}
}

// A clean request is queued (202, the operation), in dry run as Overtide
// is, and sent again with the same operation token, it's the same
// operation, not a second one.
func TestRemovalExecuteQueuesOnceInDryRun(t *testing.T) {
	fx := newRemovalFixture(t)
	request := removalRequest{Kind: "media", OperationToken: "token-1", MediaType: "movie", MediaID: 1, ServiceID: "radarr-1", ManagedFiles: []string{"radarr:10"}}
	type accepted struct {
		OperationID int64  `json:"operationId"`
		Status      string `json:"status"`
		DryRun      bool   `json:"dryRun"`
	}
	first := post(t, fx.handler, "/api/removal/execute", request, fx.session)
	if first.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", first.Code, first.Body)
	}
	got := decode[accepted](t, first)
	if got.OperationID == 0 || got.Status != "queued" || !got.DryRun {
		t.Fatalf("%+v", got)
	}
	again := decode[accepted](t, post(t, fx.handler, "/api/removal/execute", request, fx.session))
	if again.OperationID != got.OperationID {
		t.Fatalf("the same token made a second operation: %d then %d", got.OperationID, again.OperationID)
	}
	events, err := fx.database.HistoryEvents(10)
	if err != nil || len(events) != 1 || events[0].Status != "queued" || !events[0].DryRun {
		t.Fatalf("history: %+v %v", events, err)
	}
	if response := post(t, fx.handler, "/api/removal/execute", request, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", response.Code)
	}
}

// The window shows filenames, the related torrents by relationship (each
// once, why it's related), and no unassociated torrent in a media's.
func TestRemovalViewUsesFilenamesAndRelationshipGroups(t *testing.T) {
	data := removalData{
		Plan:             removal.RemovalPlan{Kind: removal.MediaObject, RequestedLabel: "Test Movie"},
		ManagedFileCount: 1, ManagedSectionLabel: "Movie files", SelectedActions: 1,
		ManagedGroups: []managedRemovalGroup{{Label: "Files", Files: []managedRemovalFile{{
			Ref: model.MediaFileRef{Source: "radarr", SourceFileID: 9}, File: model.File{SizeBytes: 1024},
			Selected: true, Filename: "actual-file.mkv", Label: "actual-file.mkv", PhysicalKey: "1:2",
		}}}},
		RelatedGroups: []relatedRemovalTorrentGroup{
			{Label: "Current", Torrents: []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "abc", Name: "Current Release", AssociationStatus: model.TorrentCurrent}, Selected: true, Selectable: true, PhysicallyBacks: true, FileCount: 1}}},
			{Label: "Superseded", Torrents: []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "old", Name: "Old Release", AssociationStatus: model.TorrentSuperseded}, Selectable: true, FileCount: 1}}},
		},
		RelatedTorrentCount: 2, SelectedTorrentCount: 1, PreservedTorrentCount: 1,
		MediaType: string(model.Movie), MediaID: 1,
	}
	view := removalViewOf(data)
	if view.ManagedSectionLabel != "Movie files" || len(view.ManagedGroups) != 1 || view.ManagedGroups[0].Files[0].Filename != "actual-file.mkv" || view.ManagedGroups[0].Files[0].PhysicalKey != "1:2" {
		t.Fatalf("managed files: %+v", view.ManagedGroups)
	}
	if view.RelatedTorrentCount != 2 || view.SelectedTorrentCount != 1 || view.PreservedTorrentCount != 1 {
		t.Fatalf("counts: %+v", view)
	}
	seen := map[string]int{}
	metas := map[string]string{}
	for _, group := range view.RelatedGroups {
		for _, torrent := range group.Torrents {
			seen[torrent.Name]++
			metas[torrent.Name] = torrent.Meta
			if torrent.Name == "Unassociated" || group.Label == "Unassociated" {
				t.Fatalf("media removal shows an unassociated torrent: %+v", group)
			}
		}
	}
	if seen["Current Release"] != 1 || seen["Old Release"] != 1 {
		t.Fatalf("each torrent once: %v", seen)
	}
	if metas["Current Release"] != "physically backs this media" || metas["Old Release"] != "superseded by a later import" {
		t.Fatalf("why each is related: %v", metas)
	}
}

// A torrent's removal shows the library paths and the library files it
// shares physical data with, but offers none of them: a torrent removal may
// only remove the torrent (validateRemovalScope refuses the rest anyway).
func TestTorrentRemovalViewDisclosesButCannotSelectLibraryFiles(t *testing.T) {
	data := removalData{
		Plan: removal.RemovalPlan{Kind: removal.TorrentObject, RequestedLabel: "Afterburn", Files: []removal.FileState{
			{Path: "/data/downloads/complete/Afterburn.mkv", Owner: removal.TorrentOwner},
			{Path: "/data/Films/Afterburn (2025)/Afterburn.mkv", Owner: removal.UnmanagedOwner},
		}},
		TorrentTarget: true, TorrentSelected: true, SelectedActions: 1, Hash: "abc",
		RelatedUnmanaged: []relatedUnmanagedFile{{Path: "/data/Films/Afterburn (2025)/Afterburn.mkv", SizeBytes: 10, Selected: true}},
		RelatedManaged: []relatedManagedMedia{{Media: model.MediaRef{Type: model.Movie, ServiceID: "radarr-1", SourceID: 42, Title: "Afterburn", Year: 2025}, FileCount: 1,
			Groups: []managedRemovalGroup{{Label: "Files", Files: []managedRemovalFile{{Ref: model.MediaFileRef{Source: "radarr", SourceFileID: 1}, Filename: "Afterburn.mkv"}}}}}},
		FileGroups: []removalPhysicalFileGroup{{Paths: []string{"/data/downloads/complete/Afterburn.mkv", "/data/Films/Afterburn (2025)/Afterburn.mkv"}}},
	}
	view := removalViewOf(data)
	if !view.TorrentTarget || view.Hash != "abc" || len(view.FileGroups) != 1 || view.FileGroups[0].PathCount != 2 || view.PathCount != 2 {
		t.Fatalf("torrent window: %+v", view)
	}
	if len(view.RelatedUnmanaged) != 1 || view.RelatedUnmanaged[0].Selectable || view.RelatedUnmanaged[0].Selected {
		t.Fatalf("a related library path may be chosen: %+v", view.RelatedUnmanaged)
	}
	if len(view.RelatedManaged) != 1 || len(view.RelatedManaged[0].Groups) != 0 {
		t.Fatalf("a hardlinked library file may be chosen: %+v", view.RelatedManaged)
	}
	if m := view.RelatedManaged[0]; m.Title != "Afterburn" || m.Year != 2025 || m.Type != model.Movie || m.SourceID != 42 || m.ServiceID != "radarr-1" {
		t.Fatalf("the hardlinked film isn't named: %+v", m)
	}
}

// Choosing what to remove recalculates the consequences in the browser,
// from the facts the window was given: the calculation never asks the
// server.
func TestRemovalConsequencesAreCalculatedLocally(t *testing.T) {
	source, err := os.ReadFile("../../frontend/src/parts/removal/selection.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"export function consequences(", "export function selectedActions(", "physicalKey"} {
		if !bytes.Contains(source, []byte(expected)) {
			t.Fatalf("the removal calculator lacks %q", expected)
		}
	}
	for _, forbidden := range []string{"fetch(", "lib/api", "XMLHttpRequest", "EventSource"} {
		if bytes.Contains(source, []byte(forbidden)) {
			t.Fatalf("the removal calculator asks the network (%q)", forbidden)
		}
	}
}
