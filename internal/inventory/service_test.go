package inventory

import (
	"github.com/overfin/overtide/internal/model"
	"testing"
)

func TestMediaWithoutFilesCannotClaimCurrentTorrent(t *testing.T) {
	missing := model.Media{Type: model.Movie, SourceID: 1, Title: "Missing", SizeBytes: 0}
	if mediaHasCurrentFiles(missing) {
		t.Fatal("logical media with no files must not claim a torrent as currently associated")
	}
	present := model.Media{Type: model.Movie, SourceID: 2, Title: "Present", SizeBytes: 1}
	if !mediaHasCurrentFiles(present) {
		t.Fatal("media with current file data should be eligible to claim its current torrent")
	}
}

func TestProjectTorrentRelationsIncludesHistoricalTorrent(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 10, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 10, Title: "Example"}
	torrents := []model.Torrent{
		{Hash: "current", Name: "Current", AssociationStatus: "ASSOCIATED", MediaItems: []model.MediaRef{ref}},
		{Hash: "old", Name: "Old", AssociationStatus: "SUPERSEDED", FormerMediaItems: []model.MediaRef{ref}},
	}
	projectTorrentRelations(media, torrents)
	if len(media[0].Torrents) != 2 {
		t.Fatalf("expected current and historical torrent on media, got %#v", media[0].Torrents)
	}
	if media[0].Torrents[0].Hash != "current" || media[0].Torrents[1].Hash != "old" {
		t.Fatalf("unexpected projected torrents: %#v", media[0].Torrents)
	}
}

func TestPhysicalBackingPromotesTorrentToCurrent(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 7, Title: "Example"}
	torrents := []model.Torrent{{Hash: "linked", AssociationStatus: model.TorrentSuperseded, FormerMediaItems: []model.MediaRef{ref}}}
	files := []model.File{
		{Path: "/media/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 2},
		{Path: "/downloads/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 2},
	}
	mediaRefs := []model.MediaFileRef{{MediaType: model.Movie, MediaID: 7, Path: "/media/example.mkv"}}
	torrentRefs := []model.TorrentFileRef{{Hash: "linked", Path: "/downloads/example.mkv"}}
	applyTorrentMediaHardlinks(torrents, media, files, mediaRefs, torrentRefs)
	if torrents[0].AssociationStatus != model.TorrentCurrent || !containsMediaRef(torrents[0].MediaItems, ref) {
		t.Fatalf("physical backing did not promote torrent: %#v", torrents[0])
	}
	if containsMediaRef(torrents[0].FormerMediaItems, ref) || !containsMediaRef(torrents[0].HardlinkedMediaItems, ref) {
		t.Fatalf("promoted relationship facts are inconsistent: %#v", torrents[0])
	}
}

func TestCurrentCopiedTorrentRemainsCurrentWithoutHardlink(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 7, Title: "Example"}
	torrents := []model.Torrent{{Hash: "copied", AssociationStatus: model.TorrentCurrent, MediaItems: []model.MediaRef{ref}}}
	files := []model.File{
		{Path: "/media/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 1},
		{Path: "/downloads/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 10, Links: 1},
	}
	applyTorrentMediaHardlinks(torrents, media, files,
		[]model.MediaFileRef{{MediaType: model.Movie, MediaID: 7, Path: "/media/example.mkv"}},
		[]model.TorrentFileRef{{Hash: "copied", Path: "/downloads/example.mkv"}},
	)
	if torrents[0].AssociationStatus != model.TorrentCurrent || !containsMediaRef(torrents[0].MediaItems, ref) || containsMediaRef(torrents[0].HardlinkedMediaItems, ref) {
		t.Fatalf("copied current relationship changed incorrectly: %#v", torrents[0])
	}
}

func TestApplyMediaBundleEstimatesCombinesHardlinkedTorrentBytes(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 7, Title: "Example"}
	torrents := []model.Torrent{{Hash: "linked", AssociationStatus: model.TorrentSuperseded, FormerMediaItems: []model.MediaRef{ref}}}
	files := []model.File{
		{Path: "/media/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 2, SizeBytes: 1000},
		{Path: "/downloads/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 2, SizeBytes: 1000},
	}
	mediaRefs := []model.MediaFileRef{{MediaType: model.Movie, MediaID: 7, Path: "/media/example.mkv"}}
	torrentRefs := []model.TorrentFileRef{{Hash: "linked", Path: "/downloads/example.mkv"}}

	// applyTorrentMediaHardlinks promotes the torrent to Current and publishes
	// HardlinkedMediaItems, exactly as the real reconciliation flow does
	// before either estimate function runs.
	applyTorrentMediaHardlinks(torrents, media, files, mediaRefs, torrentRefs)
	applyMediaFileEstimates(media, files, mediaRefs)
	applyMediaBundleEstimates(media, torrents, files, mediaRefs, torrentRefs)

	if !media[0].ReclaimableKnown || media[0].ReclaimableBytes != 0 {
		t.Fatalf("expected the media alone to report zero reclaimable bytes while the torrent still holds a hardlink: %#v", media[0])
	}
	if !media[0].BundleReclaimableKnown || media[0].BundleReclaimableBytes != 1000 {
		t.Fatalf("expected the bundle estimate to report the full shared size once its hardlinked torrent is included: %#v", media[0])
	}
}

func TestApplyMediaBundleEstimatesIgnoresNonHardlinkedCurrentTorrent(t *testing.T) {
	media := []model.Media{{Type: model.Movie, SourceID: 7, Title: "Example"}}
	ref := model.MediaRef{Type: model.Movie, SourceID: 7, Title: "Example"}
	torrents := []model.Torrent{{Hash: "copied", AssociationStatus: model.TorrentCurrent, MediaItems: []model.MediaRef{ref}}}
	files := []model.File{
		{Path: "/media/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 9, Links: 1, SizeBytes: 1000},
		{Path: "/downloads/example.mkv", Exists: true, IdentityKnown: true, Device: 1, Inode: 10, Links: 1, SizeBytes: 1000},
	}
	mediaRefs := []model.MediaFileRef{{MediaType: model.Movie, MediaID: 7, Path: "/media/example.mkv"}}
	torrentRefs := []model.TorrentFileRef{{Hash: "copied", Path: "/downloads/example.mkv"}}

	applyTorrentMediaHardlinks(torrents, media, files, mediaRefs, torrentRefs)
	applyMediaFileEstimates(media, files, mediaRefs)
	applyMediaBundleEstimates(media, torrents, files, mediaRefs, torrentRefs)

	if media[0].BundleReclaimableBytes != media[0].ReclaimableBytes {
		t.Fatalf("a separate copy must never be folded into the bundle estimate: %#v", media[0])
	}
}
