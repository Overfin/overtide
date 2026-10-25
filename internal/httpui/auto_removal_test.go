package httpui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/removal"
	"github.com/overfin/overtide/internal/store"
)

// TestActionIsUnassociatedTorrentTreatsFormerRelationshipAsKnown guards the
// distinction between "Overtide has never had any relationship for this
// torrent at all" (the case automatic removal excludes by default, since it
// may be a personal download or from an untracked service) and "this
// torrent was managed once but its relationship is now historical" — the
// same kind of provenance a Superseded torrent already relies on, not the
// unknown case this gate targets.
func TestActionIsUnassociatedTorrentTreatsFormerRelationshipAsKnown(t *testing.T) {
	cases := []struct {
		name   string
		action cleanup.Action
		want   bool
	}{
		{
			name:   "truly unassociated, no history at all",
			action: cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{AssociationStatus: model.TorrentUnassociated}}},
			want:   true,
		},
		{
			// Orphaned means import provenance exists (FormerMediaItems
			// records what) but nothing proves a specific replacement — a
			// distinct, named state from Unassociated precisely so this case
			// isn't lumped in with "no relationship at all."
			name:   "orphaned: has a former media relationship, no specific replacement",
			action: cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{AssociationStatus: model.TorrentOrphaned, FormerMediaItems: []model.MediaRef{{Title: "Old Show"}}}}},
			want:   false,
		},
		{
			name:   "superseded",
			action: cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{AssociationStatus: model.TorrentSuperseded}}},
			want:   false,
		},
		{
			name:   "current, independent copy",
			action: cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: false}}},
			want:   false,
		},
		{
			name:   "not a torrent action at all",
			action: cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{}},
			want:   false,
		},
	}
	for _, tc := range cases {
		if got := actionIsUnassociatedTorrent(tc.action); got != tc.want {
			t.Errorf("%s: actionIsUnassociatedTorrent = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestActionIsIncompleteTorrent(t *testing.T) {
	cases := []struct {
		name   string
		action cleanup.Action
		want   bool
	}{
		{
			name:   "incomplete, bytes still left to download",
			action: cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{AmountLeftBytes: 100}}},
			want:   true,
		},
		{
			name:   "complete, nothing left to download",
			action: cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{AmountLeftBytes: 0}}},
			want:   false,
		},
		{
			name:   "not a torrent action at all",
			action: cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{}},
			want:   false,
		},
	}
	for _, tc := range cases {
		if got := actionIsIncompleteTorrent(tc.action); got != tc.want {
			t.Errorf("%s: actionIsIncompleteTorrent = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRunAutoRemovalEvaluationNoOpsWhenGloballyDisabled(t *testing.T) {
	for _, mode := range []string{config.RemovalAutoDisabled, "", "bogus-value"} {
		server := &Server{inv: inventory.New(config.Config{Removal: config.RemovalConfig{AutoMode: mode}}, nil)}
		if err := server.runAutoRemovalEvaluation(context.TODO()); err != nil {
			t.Fatalf("mode=%q: expected disabled (including empty/unrecognized) to short-circuit cleanly, got %v", mode, err)
		}
	}
}

func newTestInventoryWithReconciledState(t *testing.T) *inventory.Service {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	files := []model.File{
		{Path: "/movies/a.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 1, Links: 1, SizeBytes: 100},
	}
	mediaRefs := []model.MediaFileRef{
		{ServiceID: "radarr-1", ServiceName: "Movies", MediaType: model.Movie, MediaID: 1, Source: "radarr", SourceFileID: 10, Path: "/movies/a.mkv"},
	}
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Test Movie", ServiceID: "radarr-1", ServiceName: "Movies"}}
	torrents := []model.Torrent{
		{Hash: "bundlehash", Name: "Bundled Release", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, ServiceID: "qbittorrent-1", Client: "Downloader"},
		{Hash: "oldhash", Name: "Old Release", AssociationStatus: model.TorrentSuperseded, ServiceID: "qbittorrent-1", Client: "Downloader"},
	}
	if err := database.PublishReconciliation(1, files, mediaRefs, nil, nil, torrents, media); err != nil {
		t.Fatal(err)
	}
	return inventory.New(config.Config{}, database)
}

func TestFormForActionStandaloneMediaRoundTripsThroughAdmitRemoval(t *testing.T) {
	server := &Server{inv: newTestInventoryWithReconciledState(t)}
	action := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test Movie"}}
	form, err := server.formForAction(action)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("kind") != "media" || form.Get("media_type") != "movie" || form.Get("media_id") != "1" || form.Get("managed_file") != "radarr:10" {
		t.Fatalf("unexpected form: %#v", form)
	}
	admission, err := server.admitRemoval(form)
	if err != nil {
		t.Fatalf("constructed form was rejected by admitRemoval: %v", err)
	}
	if admission.Kind != removal.MediaObject || admission.Key != "movie:1:radarr-1" || admission.Label != "Test Movie" {
		t.Fatalf("unexpected admission: %#v", admission)
	}
	if admission.ServiceName != "Movies" {
		t.Fatalf("expected ServiceName to carry through to admission, got %q", admission.ServiceName)
	}
}

func TestFormForActionHardlinkedBundleIncludesTorrentAndRoundTrips(t *testing.T) {
	server := &Server{inv: newTestInventoryWithReconciledState(t)}
	torrent := model.Torrent{Hash: "bundlehash", Name: "Bundled Release"}
	action := cleanup.Action{Kind: cleanup.HardlinkedBundle, Media: model.Media{Type: model.Movie, SourceID: 1, Title: "Test Movie"}, Torrents: []model.Torrent{torrent}}
	form, err := server.formForAction(action)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("kind") != "media" || form.Get("torrent") != "bundlehash" || form.Get("managed_file") != "radarr:10" {
		t.Fatalf("unexpected bundle form: %#v", form)
	}
	if _, err := server.admitRemoval(form); err != nil {
		t.Fatalf("constructed bundle form was rejected by admitRemoval: %v", err)
	}
}

func TestFormForActionStandaloneTorrentRoundTripsThroughAdmitRemoval(t *testing.T) {
	server := &Server{inv: newTestInventoryWithReconciledState(t)}
	action := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Hash: "OldHash", Name: "Old Release"}}}
	form, err := server.formForAction(action)
	if err != nil {
		t.Fatal(err)
	}
	if form.Get("kind") != "torrent" || form.Get("hash") != "oldhash" || form.Get("target") != "1" {
		t.Fatalf("unexpected torrent form: %#v", form)
	}
	admission, err := server.admitRemoval(form)
	if err != nil {
		t.Fatalf("constructed form was rejected by admitRemoval: %v", err)
	}
	if admission.Kind != removal.TorrentObject || admission.Key != "oldhash" {
		t.Fatalf("unexpected admission: %#v", admission)
	}
	if admission.ServiceName != "Downloader" {
		t.Fatalf("expected the torrent's Client to carry through as ServiceName, got %q", admission.ServiceName)
	}
}

func TestFormForActionErrorsWhenMediaHasNoManagedFiles(t *testing.T) {
	server := &Server{inv: newTestInventoryWithReconciledState(t)}
	action := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{Type: model.Movie, SourceID: 999, Title: "Missing"}}
	if _, err := server.formForAction(action); err == nil {
		t.Fatal("expected an error for a media action with no managed files")
	}
}

// A cross-seed group is removed as one removal per torrent; other actions
// stay whole.
func TestExpandGroup(t *testing.T) {
	group := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Hash: "a"}, {Hash: "b"}}}
	singles := expandGroup(group)
	if len(singles) != 2 || singles[0].Torrents[0].Hash != "a" || singles[1].Torrents[0].Hash != "b" || len(singles[1].Torrents) != 1 {
		t.Fatalf("%#v", singles)
	}
	bundle := cleanup.Action{Kind: cleanup.HardlinkedBundle, Torrents: []model.Torrent{{Hash: "a"}, {Hash: "b"}}}
	if got := expandGroup(bundle); len(got) != 1 || len(got[0].Torrents) != 2 {
		t.Fatalf("a bundle stays whole: %#v", got)
	}
}

// A group counts as having no known owner only if none of its torrents has
// one.
func TestUnassociatedGroup(t *testing.T) {
	mixed := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{AssociationStatus: model.TorrentSuperseded}, {AssociationStatus: model.TorrentUnassociated}}}
	if actionIsUnassociatedTorrent(mixed) {
		t.Fatal("a superseded torrent gives the group a known owner")
	}
	alone := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{AssociationStatus: model.TorrentUnassociated}, {AssociationStatus: model.TorrentUnassociated}}}
	if !actionIsUnassociatedTorrent(alone) {
		t.Fatal("expected no known owner")
	}
}

// A cross-seed group's figure counts once, on its lowest hash.
func TestReclaimableOnce(t *testing.T) {
	a := model.Torrent{Hash: "aaa", SharesFilesWith: []string{"bbb"}, GroupReclaimableKnown: true, GroupReclaimableBytes: 1000}
	b := model.Torrent{Hash: "bbb", SharesFilesWith: []string{"aaa"}, GroupReclaimableKnown: true, GroupReclaimableBytes: 1000}
	gone := model.Torrent{Hash: "ccc", FilesMissing: true, ReclaimableBytes: 50}
	if reclaimableOnce(a)+reclaimableOnce(b)+reclaimableOnce(gone) != 1000 {
		t.Fatalf("%d %d %d", reclaimableOnce(a), reclaimableOnce(b), reclaimableOnce(gone))
	}
}
