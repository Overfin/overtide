package httpui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/removal"
	"github.com/overfin/overtide/internal/store"
	"github.com/overfin/overtide/internal/tasks"
)

// loginForTest makes the admin account through the real first-run setup
// (POST /api/setup) and returns the session it issues, so a
// Handler()-level test on a server backed by a real store (which authGate
// requires a session for) can act as a signed-in browser.
func loginForTest(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	return setUp(t, handler)
}

func TestTorrentRemovalRejectsInjectedLibraryPath(t *testing.T) {
	form := url.Values{
		"kind":           {"torrent"},
		"hash":           {"0ce683820c305af1aaca0c1fde91f08eccb9e374"},
		"target":         {"1"},
		"unmanaged_path": {"/data/Films/Afterburn (2025)/Afterburn.mkv"},
	}
	if err := validateRemovalScope(form); err == nil {
		t.Fatal("torrent removal accepted an injected unmanaged library path")
	}
}

func TestScheduledExecutionRejectsInjectedLibraryPathBeforeInventoryAccess(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"kind":           {"torrent"},
		"hash":           {"abc"},
		"target":         {"1"},
		"unmanaged_path": {"/data/Films/Afterburn (2025)/Afterburn.mkv"},
	}
	request := &http.Request{Method: http.MethodPost, Form: form}
	response := httptest.NewRecorder()
	server.executeRemovalNowContext(response, request, context.Background())
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "scope rejected") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestTorrentRemovalScopeAcceptsOnlyTorrentTarget(t *testing.T) {
	form := url.Values{"kind": {"torrent"}, "hash": {"abc"}, "target": {"1"}}
	if err := validateRemovalScope(form); err != nil {
		t.Fatalf("clean torrent removal was rejected: %v", err)
	}
}

func TestMediaRemovalScopeAcceptsRelatedTorrentAction(t *testing.T) {
	form := url.Values{"kind": {"media"}, "managed_file": {"radarr:9"}, "torrent": {"abc"}}
	if err := validateRemovalScope(form); err != nil {
		t.Fatalf("media removal rejected a related torrent action: %v", err)
	}
}

func TestEveryRemovalKindRejectsCrossOwnerActions(t *testing.T) {
	for name, form := range map[string]url.Values{
		"media with direct path":   {"kind": {"media"}, "unmanaged_path": {"/data/file"}},
		"unmanaged with torrent":   {"kind": {"unmanaged"}, "path": {"/data/file"}, "torrent": {"abc"}},
		"torrent with media owner": {"kind": {"torrent"}, "target": {"1"}, "managed_file": {"radarr:1"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateRemovalScope(form); err == nil {
				t.Fatalf("cross-owner form was accepted: %#v", form)
			}
		})
	}
}

// TestUnmanagedRemovalScopeAcceptsCleanForm guards the re-enablement of
// direct filesystem removal for Unmanaged files: a clean "unmanaged" kind
// submission carrying only "path" must be accepted, not rejected outright
// the way it was while this feature was disabled pending refactoring.
func TestUnmanagedRemovalScopeAcceptsCleanForm(t *testing.T) {
	form := url.Values{"kind": {"unmanaged"}, "path": {"/data/Films/Agent Zeta/movie.mkv"}}
	if err := validateRemovalScope(form); err != nil {
		t.Fatalf("clean unmanaged removal was rejected: %v", err)
	}
}

func TestUnmanagedRemovalScopeRequiresAtLeastOnePath(t *testing.T) {
	form := url.Values{"kind": {"unmanaged"}}
	if err := validateRemovalScope(form); err == nil {
		t.Fatal("expected an error when no path is selected")
	}
}

func TestCrossOriginWriteIsRejected(t *testing.T) {
	h := sameOriginWrites(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	r := httptest.NewRequest(http.MethodPost, "http://overtide.local/removal/execute", nil)
	r.Host = "overtide.local"
	r.Header.Set("Origin", "http://evil.local")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestSelectedUnmanagedStatesPreservesConfirmedIdentity(t *testing.T) {
	p := removal.RemovalPlan{Files: []removal.FileState{
		{Path: "/data/a", Owner: removal.UnmanagedOwner, Selected: true, Exists: true, IdentityKnown: true, Device: 7, Inode: 9},
		{Path: "/data/b", Owner: removal.UnmanagedOwner, Selected: false},
	}}
	xs := selectedUnmanagedStates(p, []string{"/data/a", "/data/b"})
	if len(xs) != 1 || xs[0].Device != 7 || xs[0].Inode != 9 {
		t.Fatalf("states=%#v", xs)
	}
}

func TestGroupRelatedTorrentsSeparatesRelationshipTypes(t *testing.T) {
	groups := groupRelatedTorrents([]relatedRemovalTorrent{
		{Torrent: model.Torrent{Hash: "old", Name: "Old", AssociationStatus: "SUPERSEDED"}},
		{Torrent: model.Torrent{Hash: "current", Name: "Current", AssociationStatus: "ASSOCIATED"}, Selected: true},
	})
	if len(groups) != 2 || groups[0].Label != "Current" || groups[1].Label != "Superseded" {
		t.Fatalf("unexpected groups: %#v", groups)
	}
	if len(groups[0].Torrents) != 1 || !groups[0].Torrents[0].Selected || groups[0].Torrents[0].Torrent.Hash != "current" {
		t.Fatalf("current group lost torrent state: %#v", groups[0])
	}
}

func TestStorageGuidanceExplainsBlockingAssociatedTorrent(t *testing.T) {
	plan := removal.RemovalPlan{Files: []removal.FileState{
		{Path: "/library/movie.mkv", Owner: removal.MediaOwner, Selected: true, Exists: true, SizeBytes: 4096, IdentityKnown: true, Device: 1, Inode: 2, Links: 2},
		{Path: "/downloads/movie.mkv", Owner: removal.TorrentOwner, OwnerKey: "abc", Selected: false, Exists: true, SizeBytes: 4096, IdentityKnown: true, Device: 1, Inode: 2, Links: 2},
	}}
	title, action := storageGuidance(plan, model.Movie, []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "abc", AssociationStatus: "ASSOCIATED"}}})
	if title == "" || action != "It is hardlinked to the current torrent. Also select that torrent below to reclaim the shared data." {
		t.Fatalf("unexpected guidance: %q / %q", title, action)
	}
	plan.Files[1].Selected = true
	if title, action := storageGuidance(plan, model.Movie, []relatedRemovalTorrent{{Torrent: model.Torrent{Hash: "abc", AssociationStatus: "ASSOCIATED"}, Selected: true}}); title != "" || action != "" {
		t.Fatalf("guidance must disappear when the final link is selected: %q / %q", title, action)
	}
}

func selectedTorrent(torrents []relatedRemovalTorrent, hash string) bool {
	for _, torrent := range torrents {
		if torrent.Torrent.Hash == hash {
			return torrent.Selected
		}
	}
	return false
}

func TestFilterMedia(t *testing.T) {
	items := []model.Media{
		{Type: model.Movie, SourceID: 1, Title: "Alien", Torrents: []model.Torrent{{Hash: "a", AssociationStatus: model.TorrentCurrent}}, Tags: []string{"keep"}},
		{Type: model.Series, SourceID: 2, Title: "Severance"},
	}
	got := filterMedia(items, "alien", "movie", "any", nil, "yes", true)
	if len(got) != 1 || got[0].Title != "Alien" {
		t.Fatalf("unexpected media filter result: %#v", got)
	}
	got = filterMedia(items, "", "series", "any", nil, "no", true)
	if len(got) != 1 || got[0].Title != "Severance" {
		t.Fatalf("unexpected series filter result: %#v", got)
	}
}

func TestFilterTorrents(t *testing.T) {
	items := []model.Torrent{
		{Hash: "abc", Name: "Old Release", AssociationStatus: "SUPERSEDED", ReclaimableKnown: true, ReclaimableBytes: 1024, LeechersConnected: 0},
		{Hash: "def", Name: "Current Release", AssociationStatus: "ASSOCIATED", LeechersConnected: 1},
	}
	got := filterTorrents(items, "old", "SUPERSEDED", "positive", "inactive")
	if len(got) != 1 || got[0].Hash != "abc" {
		t.Fatalf("unexpected torrent filter result: %#v", got)
	}
	got = filterTorrents(items, "def", "ASSOCIATED", "any", "active")
	if len(got) != 1 || got[0].Hash != "def" {
		t.Fatalf("unexpected active torrent filter result: %#v", got)
	}
}

func TestApplyUnmanagedSummaryBeforeFirstScan(t *testing.T) {
	var d homeData
	applyUnmanagedSummary(&d, nil, time.Time{}, nil)
	if d.UnmanagedAvailable {
		t.Fatal("unmanaged data must not be authoritative before first successful scan")
	}
	if d.UnmanagedError != "" {
		t.Fatalf("startup without a completed scan is not an error: %q", d.UnmanagedError)
	}
}

func TestApplyUnmanagedSummaryError(t *testing.T) {
	var d homeData
	applyUnmanagedSummary(&d, nil, time.Time{}, errors.New("qBittorrent unavailable"))
	if d.UnmanagedError != "qBittorrent unavailable" {
		t.Fatalf("unexpected error: %q", d.UnmanagedError)
	}
}

func TestFilterMediaHidesNoFileMediaByDefault(t *testing.T) {
	items := []model.Media{
		{Type: model.Movie, SourceID: 1, Title: "Present", SizeBytes: 1024},
		{Type: model.Movie, SourceID: 2, Title: "Missing", SizeBytes: 0},
	}
	got := filterMedia(items, "", "any", "any", nil, "any", false)
	if len(got) != 1 || got[0].Title != "Present" {
		t.Fatalf("unexpected default file filter: %#v", got)
	}
	got = filterMedia(items, "", "any", "any", nil, "any", true)
	if len(got) != 2 {
		t.Fatalf("show-no-files should include both media, got %d", len(got))
	}
}

func TestTaskGroupsHidesUnconfiguredServices(t *testing.T) {
	all := []tasks.Status{
		{ID: "inventory", Name: "Base inventory"},
		{ID: "files", Name: "File reconciliation"},
		{ID: removalTaskID, Name: "Removal operations"},
		{ID: autoRemovalTaskID, Name: "Automatic removal"},
		{ID: "overfin", Name: "Overfin scores"},
	}

	server := &Server{inv: inventory.New(config.Config{}, nil)}
	groups := server.taskGroups(all)
	if len(groups) != 1 || groups[0].Name != "Library & Storage" {
		t.Fatalf("expected only the core group with neither service configured, got %#v", groups)
	}
	if len(groups[0].Tasks) != 4 {
		t.Fatalf("expected all four core tasks grouped together, got %#v", groups[0].Tasks)
	}

	cfg := config.Config{}
	cfg.Overfin.URL = "http://overfin"
	server = &Server{inv: inventory.New(cfg, nil)}
	groups = server.taskGroups(all)
	if len(groups) != 2 || groups[1].Name != "Overfin" || len(groups[1].Tasks) != 1 {
		t.Fatalf("expected an Overfin group once configured, got %#v", groups)
	}
}

func TestGroupMediaTorrents(t *testing.T) {
	items := []model.Torrent{
		{Hash: "s2", Name: "Z old", AssociationStatus: "SUPERSEDED"},
		{Hash: "c1", Name: "Current", AssociationStatus: "ASSOCIATED"},
		{Hash: "o1", Name: "Orphan", AssociationStatus: "ORPHANED"},
		{Hash: "s1", Name: "A old", AssociationStatus: "SUPERSEDED"},
		{Hash: "u1", Name: "Unknown", AssociationStatus: "UNASSOCIATED"},
	}
	current, superseded, orphaned, unassociated := groupMediaTorrents(items)
	if len(current) != 1 || current[0].Hash != "c1" {
		t.Fatalf("unexpected current torrents: %#v", current)
	}
	if len(superseded) != 2 || superseded[0].Hash != "s1" || superseded[1].Hash != "s2" {
		t.Fatalf("unexpected superseded torrents: %#v", superseded)
	}
	if len(orphaned) != 1 || orphaned[0].Hash != "o1" {
		t.Fatalf("unexpected orphaned torrents: %#v", orphaned)
	}
	if len(unassociated) != 1 || unassociated[0].Hash != "u1" {
		t.Fatalf("unexpected unassociated torrents: %#v", unassociated)
	}
}

func TestProvenManagedRefsForTorrentUsesPhysicalIdentity(t *testing.T) {
	d := t.TempDir()
	dl := filepath.Join(d, "download.mkv")
	media := filepath.Join(d, "media.mkv")
	copyPath := filepath.Join(d, "copy.mkv")
	if err := os.WriteFile(dl, []byte("same bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(dl, media); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, []byte("same bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	inspect := func(path string) model.File {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		st := fi.Sys().(*syscall.Stat_t)
		return model.File{Path: path, SizeBytes: fi.Size(), Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: uint64(st.Ino), Links: uint64(st.Nlink)}
	}
	files := []model.File{inspect(dl), inspect(media), inspect(copyPath)}
	refs := []model.MediaFileRef{
		{MediaType: model.Series, MediaID: 1, Source: "sonarr", SourceFileID: 10, Path: media},
		{MediaType: model.Series, MediaID: 1, Source: "sonarr", SourceFileID: 11, Path: copyPath},
	}
	trefs := []model.TorrentFileRef{{Client: "qBittorrent", Hash: "abc", FileIndex: 0, Path: dl}}
	got := provenManagedRefsForTorrent(files, refs, trefs, "ABC")
	if len(got) != 1 || got[0].SourceFileID != 10 {
		t.Fatalf("expected only hardlinked managed file, got %#v", got)
	}
}

func TestGroupRemovalFilesGroupsHardlinksByPhysicalIdentity(t *testing.T) {
	files := []removal.FileState{
		{Path: "/data/downloads/a.mkv", Owner: removal.TorrentOwner, OwnerKey: "abc", Selected: true, Selectable: true, Exists: true, SizeBytes: 123, IdentityKnown: true, Device: 56, Inode: 99, Links: 2},
		{Path: "/data/Films/a.mkv", Owner: removal.MediaOwner, OwnerKey: "radarr:9", Selected: true, Selectable: true, Exists: true, SizeBytes: 123, IdentityKnown: true, Device: 56, Inode: 99, Links: 2},
	}
	groups := groupRemovalFiles(removal.RemovalPlan{Kind: removal.MediaObject, Files: files}, nil)
	if len(groups) != 1 {
		t.Fatalf("expected 1 physical group, got %d", len(groups))
	}
	if len(groups[0].Paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(groups[0].Paths))
	}
	if groups[0].SizeBytes != 123 || groups[0].Links != 2 {
		t.Fatalf("unexpected group: %+v", groups[0])
	}
	if !groups[0].Selectable || !groups[0].Selected || len(groups[0].DisplayPaths) != 2 {
		t.Fatalf("physical owner actions=%+v", groups[0])
	}
}

// TestGroupRemovalFilesMakesUnmanagedTargetSelectable guards the actual gap
// that kept standalone Unmanaged removal broken even after validateRemovalScope's
// blanket rejection was lifted: groupRemovalFiles had no switch case at all
// for removal.UnmanagedOwner, so every target file fell into the
// "not selectable" branch and rendered as "Preserved · not owned by this
// operation" regardless of what buildUnmanagedRemovalPlan intended.
func TestGroupRemovalFilesMakesUnmanagedTargetSelectable(t *testing.T) {
	files := []removal.FileState{
		{Path: "/data/downloads/orphan.iso", Owner: removal.UnmanagedOwner, OwnerKey: "/data/downloads/orphan.iso", Selected: true, Selectable: true, Exists: true, SizeBytes: 999},
	}
	groups := groupRemovalFiles(removal.RemovalPlan{Kind: removal.UnmanagedObject, Files: files}, nil)
	if len(groups) != 1 || len(groups[0].DisplayPaths) != 1 {
		t.Fatalf("expected 1 group with 1 display path, got %+v", groups)
	}
	display := groups[0].DisplayPaths[0]
	if !display.Selectable || !display.Selected {
		t.Fatalf("expected the unmanaged target to be selectable and selected, got %+v", display)
	}
	if display.ActionName != "path" || display.ActionValue != "/data/downloads/orphan.iso" {
		t.Fatalf("expected action name/value path=/data/downloads/orphan.iso, got %q=%q", display.ActionName, display.ActionValue)
	}
	if strings.Contains(display.ActionLabel, "Preserved") {
		t.Fatalf("expected the unmanaged target not to be marked Preserved, got %q", display.ActionLabel)
	}
}

// TestPrepareRemovalDataMarksUnmanagedTargetSelectable guards a second,
// separate bug in the same feature: even after groupRemovalFiles correctly
// marks the primary unmanaged target Selectable for the server-rendered
// display, prepareRemovalData builds a completely different JSON model that
// the browser actually uses to decide what gets submitted on "Remove
// selected" — removal.ts only ever includes a file in the real submission
// when both file.selectable and file.selected are true (connect(): "files
// .filter(file => file.selectable && file.selected)"). prepareRemovalData's
// UnmanagedOwner branch set Always (forcing the live preview to treat it as
// selected) but never Selectable, so the primary target was silently
// dropped from every real removal request regardless of what the preview
// showed — the file could never actually be deleted.
func TestPrepareRemovalDataMarksUnmanagedTargetSelectable(t *testing.T) {
	data := removalData{
		Plan: removal.RemovalPlan{Kind: removal.UnmanagedObject, Files: []removal.FileState{
			{Path: "/data/downloads/orphan.iso", Owner: removal.UnmanagedOwner, Selected: true, Selectable: true, Exists: true, SizeBytes: 999},
		}},
		UnmanagedPaths: []string{"/data/downloads/orphan.iso"},
	}
	if err := prepareRemovalData(&data); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(data.SelectionModel)
	if err != nil {
		t.Fatal(err)
	}
	var model removalSelectionModel
	if err := json.Unmarshal(decoded, &model); err != nil {
		t.Fatal(err)
	}
	if len(model.Files) != 1 {
		t.Fatalf("expected 1 file in the selection model, got %+v", model.Files)
	}
	fact := model.Files[0]
	if !fact.Selectable {
		t.Fatalf("expected the primary unmanaged target to be Selectable in the client-side model (browser only submits selectable+selected files), got %+v", fact)
	}
	if !fact.Always || fact.ActionName != "path" || fact.ActionValue != "/data/downloads/orphan.iso" {
		t.Fatalf("expected always=true actionName=path actionValue=/data/downloads/orphan.iso, got %+v", fact)
	}
}

func TestMediaPlanDefaultsPhysicallyHardlinkedTorrentAndRejectsUnrelatedTorrent(t *testing.T) {
	root := t.TempDir()
	mediaPath := filepath.Join(root, "Films", "Movie.mkv")
	torrentPath := filepath.Join(root, "downloads", "Movie.mkv")
	otherMediaPath := filepath.Join(root, "Films", "Other.mkv")
	otherTorrentPath := filepath.Join(root, "downloads", "Other-linked.mkv")
	unrelatedPath := filepath.Join(root, "downloads", "Other.mkv")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(torrentPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, []byte("shared movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(mediaPath, torrentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherMediaPath, []byte("other shared movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(otherMediaPath, otherTorrentPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedPath, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	inspect := func(path string) model.File {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		return model.File{Path: path, Exists: true, SizeBytes: info.Size(), IdentityKnown: true, Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Links: uint64(stat.Nlink)}
	}
	files := []model.File{inspect(mediaPath), inspect(torrentPath), inspect(otherMediaPath), inspect(otherTorrentPath), inspect(unrelatedPath)}
	mediaRefs := []model.MediaFileRef{
		{MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 9, Path: mediaPath},
		{MediaType: model.Movie, MediaID: 2, Source: "radarr", SourceFileID: 10, Path: otherMediaPath},
	}
	torrentRefs := []model.TorrentFileRef{
		{Client: "qBittorrent", Hash: "linked", FileIndex: 0, Path: torrentPath},
		{Client: "qBittorrent", Hash: "linked", FileIndex: 1, Path: otherTorrentPath},
		{Client: "qBittorrent", Hash: "unrelated", FileIndex: 0, Path: unrelatedPath},
	}
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Movie"}, {Type: model.Movie, SourceID: 2, Title: "Other"}}
	mediaRelation := model.MediaRef{Type: model.Movie, SourceID: 1, Title: "Movie"}
	torrents := []model.Torrent{
		{Hash: "linked", Name: "Linked", AssociationStatus: model.TorrentUnassociated},
		{Hash: "copied", Name: "Copied current import", AssociationStatus: model.TorrentCurrent, MediaItems: []model.MediaRef{mediaRelation}},
		{Hash: "old", Name: "Superseded release", AssociationStatus: model.TorrentSuperseded, FormerMediaItems: []model.MediaRef{mediaRelation}},
		{Hash: "unrelated", Name: "Unrelated", AssociationStatus: model.TorrentUnassociated},
	}
	if err := database.PublishInventory(1, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	if err := database.PublishReconciliation(1, files, mediaRefs, torrentRefs, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	server := &Server{inv: inventory.New(config.Config{}, database)}
	plan, err := server.buildMediaRemovalPlan(model.Movie, 1, "", false, map[string]bool{}, map[string]bool{}, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	// "old" (Superseded) is now selectable too, alongside Current — every
	// historical torrent related to this media can be selected for removal,
	// not just the ones proven Current.
	if len(plan.Related) != 3 {
		t.Fatalf("related torrents=%#v", plan.Related)
	}
	selectedByHash := map[string]bool{}
	for _, related := range plan.Related {
		selectedByHash[related.Torrent.Hash] = related.Selected
	}
	if !selectedByHash["linked"] || selectedByHash["copied"] || selectedByHash["old"] {
		t.Fatalf("only the physically-backing torrent should default selected: %#v", plan.Related)
	}
	if plan.RelatedTorrentCount != 3 || len(plan.RelatedGroups) != 2 {
		t.Fatalf("media relationship context missing Current/Superseded torrents: count=%d groups=%#v", plan.RelatedTorrentCount, plan.RelatedGroups)
	}
	for _, group := range plan.RelatedGroups {
		if group.Label == "Unassociated" {
			t.Fatalf("media removal exposed an Unassociated group: %#v", plan.RelatedGroups)
		}
	}
	if plan.Plan.ReclaimableBytes != int64(len("shared movie")) {
		t.Fatalf("reclaimable=%d", plan.Plan.ReclaimableBytes)
	}
	for _, file := range plan.Plan.Files {
		if file.OwnerKey == "radarr:10" && file.Selectable {
			t.Fatalf("another media owner's file became selectable: %#v", file)
		}
	}
	foundPreservedOwner := false
	for _, group := range plan.FileGroups {
		foundPreservedOwner = foundPreservedOwner || group.PreservedLinks > 0
	}
	if !foundPreservedOwner {
		t.Fatalf("multi-file torrent did not disclose the preserved other media owner: %#v", plan.FileGroups)
	}
	// A kept torrent must keep seeding: it's offered, not ticked.
	keptServer := &Server{inv: inventory.New(config.Config{Kept: []config.KeptItem{{Key: config.KeptTorrentKey("linked"), Name: "Linked"}}}, database)}
	keptPlan, err := keptServer.buildMediaRemovalPlan(model.Movie, 1, "", false, map[string]bool{}, map[string]bool{}, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	for _, related := range keptPlan.Related {
		if related.Torrent.Hash == "linked" && related.Selected {
			t.Fatal("a kept torrent was ticked by default")
		}
	}
	revalidated, err := server.buildMediaRemovalPlan(model.Movie, 1, "", true, selectedManagedSet([]string{"radarr:9"}), mapFromValues([]string{"linked"}), map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(revalidated.Related) != 3 || !selectedTorrent(revalidated.Related, "linked") || selectedTorrent(revalidated.Related, "copied") || selectedTorrent(revalidated.Related, "old") {
		t.Fatalf("execution revalidation discarded related torrent or selected one that wasn't submitted: %#v", revalidated.Related)
	}
	if _, err := server.buildMediaRemovalPlan(model.Movie, 1, "", true, map[string]bool{"radarr:9": true}, map[string]bool{"unrelated": true}, map[string]bool{}); err == nil || !strings.Contains(err.Error(), "not current") {
		t.Fatalf("unrelated torrent selection error=%v", err)
	}
	if _, err := server.buildMediaRemovalPlan(model.Movie, 1, "", true, map[string]bool{"radarr:10": true}, map[string]bool{}, map[string]bool{}); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("cross-media managed selection error=%v", err)
	}
}

func TestPhysicalCandidatesExposeUnmanagedHardlinkSibling(t *testing.T) {
	files := []model.File{
		{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100},
		{Path: "/series/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100},
	}
	trefs := []model.TorrentFileRef{{Client: "Downloader", Hash: "abc", FileIndex: 0, Path: "/downloads/a.mkv"}}
	cm := map[string]removal.CandidateFile{"/downloads/a.mkv": {Path: "/downloads/a.mkv", Owner: removal.TorrentOwner, OwnerKey: "abc", Selected: true}}
	got := physicalCandidates(files, nil, trefs, cm, nil, map[string]bool{"abc": true}, nil)
	var sibling removal.CandidateFile
	ok := false
	for _, candidate := range got {
		if candidate.Path == "/series/a.mkv" {
			sibling, ok = candidate, true
			break
		}
	}
	if !ok {
		t.Fatal("expected physical sibling candidate")
	}
	if sibling.Owner != removal.UnmanagedOwner {
		t.Fatalf("expected unmanaged sibling, got %s", sibling.Owner)
	}
}

func TestPhysicalCandidatesPreserveMultipleOwnerClaimsOnSamePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.mkv")
	if err := os.WriteFile(path, []byte("shared"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	files := []model.File{{Path: path, Exists: true, SizeBytes: info.Size(), IdentityKnown: true, Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Links: uint64(stat.Nlink)}}
	mediaRef := model.MediaFileRef{MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 9, Path: path}
	torrentRef := model.TorrentFileRef{Hash: "abc", Path: path}
	initial := map[string]removal.CandidateFile{}
	mergeRemovalCandidate(initial, removal.CandidateFile{Path: path, Owner: removal.MediaOwner, OwnerKey: "radarr:9", Selected: true, Selectable: true})
	got := physicalCandidates(files, []model.MediaFileRef{mediaRef}, []model.TorrentFileRef{torrentRef}, initial, map[string]bool{"radarr:9": true}, map[string]bool{"abc": true}, nil)
	owners := map[removal.FileOwner]bool{}
	for _, candidate := range got {
		owners[candidate.Owner] = true
	}
	if len(got) != 2 || !owners[removal.MediaOwner] || !owners[removal.TorrentOwner] {
		t.Fatalf("same-path claims were collapsed: %#v", got)
	}
}

func TestGroupRemovalFilesReportsMissingHardlinks(t *testing.T) {
	states := []removal.FileState{{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100, Owner: removal.TorrentOwner}}
	inventoryFiles := []model.File{{Path: "/downloads/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 2, Links: 2, SizeBytes: 100}}
	groups := groupRemovalFiles(removal.RemovalPlan{Kind: removal.TorrentObject, RequestedKey: "abc", Files: states}, inventoryFiles)
	if len(groups) != 1 || groups[0].MissingLinks != 1 {
		t.Fatalf("expected one missing hardlink, got %+v", groups)
	}
}

func TestGroupUnmanagedFilesCollapsesHardlinks(t *testing.T) {
	items := []model.UnmanagedFile{
		{Path: "/a", SizeBytes: 54321, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
		{Path: "/b", SizeBytes: 54321, Device: 7, Inode: 99, Links: 2, ReclaimableKnown: true},
	}
	groups := groupUnmanagedFiles(items)
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if got := len(groups[0].Paths); got != 2 {
		t.Fatalf("got %d paths, want 2", got)
	}
	if groups[0].SizeBytes != 54321 {
		t.Fatalf("size = %d, want 54321", groups[0].SizeBytes)
	}
	if groups[0].ReclaimableBytes != 54321 {
		t.Fatalf("reclaimable = %d, want 54321", groups[0].ReclaimableBytes)
	}
}
