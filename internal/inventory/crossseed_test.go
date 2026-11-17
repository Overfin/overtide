package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/model"
)

func file(path string, inode, links uint64, size int64) model.File {
	return model.File{Path: path, SizeBytes: size, Exists: true, IdentityKnown: true, Device: 1, Inode: inode, Links: links}
}

// A superseded torrent and its cross-seed share their file: each alone
// frees nothing, the two together free it.
func TestShareGroupOfLoneTorrents(t *testing.T) {
	files := []model.File{file("/dl/a.mkv", 10, 2, 1000), file("/xs/a.mkv", 10, 2, 1000)}
	refs := []model.TorrentFileRef{{Hash: "AAA", Path: "/dl/a.mkv"}, {Hash: "BBB", Path: "/xs/a.mkv"}}
	torrents := []model.Torrent{
		{Hash: "AAA", AssociationStatus: model.TorrentSuperseded},
		{Hash: "BBB", AssociationStatus: model.TorrentUnassociated},
	}
	applyTorrentFileEstimates(torrents, files, refs)
	applyTorrentMediaHardlinks(torrents, nil, files, nil, refs)
	for _, tr := range torrents {
		if tr.ReclaimableBytes != 0 || len(tr.SharesFilesWith) != 1 || !tr.GroupReclaimableKnown || tr.GroupReclaimableBytes != 1000 {
			t.Fatalf("%s: %+v", tr.Hash, tr)
		}
	}
	if torrents[1].AssociationStatus != model.TorrentUnassociated {
		t.Fatalf("a lone group stays lone: %+v", torrents[1])
	}
}

// A cross-seed of a torrent that backs a title joins that title's bundle.
func TestCrossSeedOfCurrentTorrentJoinsItsTitle(t *testing.T) {
	files := []model.File{
		file("/media/Film/film.mkv", 20, 3, 5000),
		file("/dl/film.mkv", 20, 3, 5000),
		file("/xs/film.mkv", 20, 3, 5000),
	}
	torrentRefs := []model.TorrentFileRef{{Hash: "cur", Path: "/dl/film.mkv"}, {Hash: "xs", Path: "/xs/film.mkv"}}
	mediaRefs := []model.MediaFileRef{{MediaType: model.Movie, ServiceID: "r1", MediaID: 7, Path: "/media/Film/film.mkv"}}
	media := []model.Media{{Type: model.Movie, ServiceID: "r1", SourceID: 7, Title: "Film"}}
	torrents := []model.Torrent{
		{Hash: "cur", AssociationStatus: model.TorrentCurrent, MediaItems: []model.MediaRef{{Type: model.Movie, ServiceID: "r1", SourceID: 7}}},
		{Hash: "xs", AssociationStatus: model.TorrentUnassociated},
	}
	applyTorrentFileEstimates(torrents, files, torrentRefs)
	applyTorrentMediaHardlinks(torrents, media, files, mediaRefs, torrentRefs)
	xs := torrents[1]
	if xs.AssociationStatus != model.TorrentCurrent || len(xs.HardlinkedMediaItems) != 1 {
		t.Fatalf("the cross-seed should back the title too: %+v", xs)
	}
	applyMediaBundleEstimates(media, torrents, files, mediaRefs, torrentRefs)
	if !media[0].BundleReclaimableKnown || media[0].BundleReclaimableBytes != 5000 {
		t.Fatalf("the title's bundle should free the file with both torrents: %+v", media[0])
	}
}

// Files reconciled and gone: the torrent takes no space.
func TestTorrentWithFilesGone(t *testing.T) {
	files := []model.File{{Path: "/dl/gone.mkv", Exists: false}}
	refs := []model.TorrentFileRef{{Hash: "gone", Path: "/dl/gone.mkv"}}
	torrents := []model.Torrent{{Hash: "gone", SizeBytes: 2000, Progress: 1, AssociationStatus: model.TorrentSuperseded}}
	applyTorrentFileEstimates(torrents, files, refs)
	if !torrents[0].FilesMissing || torrents[0].ReclaimableBytes != 0 || torrents[0].StorageError != "" {
		t.Fatalf("%+v", torrents[0])
	}
}

// A download not done has no files to miss at its final path: nothing yet,
// or its data in qBittorrent's folder for incomplete downloads. Only
// qBittorrent saying its files are missing counts then.
func TestDownloadUnderWayIsNotMissingItsFiles(t *testing.T) {
	files := []model.File{{Path: "/dl/new.mkv", Exists: false}}
	refs := []model.TorrentFileRef{{Hash: "new", Path: "/dl/new.mkv"}, {Hash: "half", Path: "/dl/new.mkv"}, {Hash: "lost", Path: "/dl/new.mkv"}}
	torrents := []model.Torrent{
		{Hash: "new", SizeBytes: 2000, State: "stalledDL", AssociationStatus: model.TorrentCurrent},
		{Hash: "half", SizeBytes: 2000, Progress: 0.5, State: "downloading", AssociationStatus: model.TorrentCurrent},
		{Hash: "lost", SizeBytes: 2000, Progress: 0.5, State: "missingFiles", AssociationStatus: model.TorrentCurrent},
	}
	applyTorrentFileEstimates(torrents, files, refs)
	if torrents[0].FilesMissing || torrents[1].FilesMissing || !torrents[2].FilesMissing {
		t.Fatalf("new %t, half %t, lost %t", torrents[0].FilesMissing, torrents[1].FilesMissing, torrents[2].FilesMissing)
	}
}

// Under a reachable root, a torrent file the walk didn't find is recorded
// as missing; one under a root that isn't reachable isn't judged.
func TestMissingTorrentFiles(t *testing.T) {
	root := t.TempDir()
	refs := []model.TorrentFileRef{
		{Hash: "a", Path: filepath.Join(root, "gone.mkv")},
		{Hash: "b", Path: filepath.Join(root, "found.mkv")},
		{Hash: "c", Path: "/nowhere/elsewhere.mkv"},
	}
	found := map[string]bool{filepath.Join(root, "found.mkv"): true}
	missing := missingTorrentFiles(refs, found, []storageRoot{{Path: root}, {Path: "/nowhere"}})
	if len(missing) != 1 || missing[0].Path != filepath.Join(root, "gone.mkv") || missing[0].Exists {
		t.Fatalf("%#v", missing)
	}
}

// A root where the walk found nothing (an unmounted share's empty mount
// point) marks nothing missing.
func TestMissingTorrentFilesIgnoresAnEmptyRoot(t *testing.T) {
	root := t.TempDir()
	refs := []model.TorrentFileRef{{Hash: "a", Path: filepath.Join(root, "gone.mkv")}}
	if missing := missingTorrentFiles(refs, map[string]bool{}, []storageRoot{{Path: root}}); len(missing) != 0 {
		t.Fatalf("%#v", missing)
	}
}

// Before removing a torrent whose files are missing, they're checked again:
// back on disk, or under an empty root, it's left alone.
func TestFilesStillMissing(t *testing.T) {
	root := t.TempDir()
	roots := []storageRoot{{Path: root}}
	gone := filepath.Join(root, "gone.mkv")
	if got := filesStillMissing([]string{gone}, roots); !strings.Contains(got, "is it mounted") {
		t.Fatalf("empty root: %q", got)
	}
	if err := os.WriteFile(filepath.Join(root, "other.mkv"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := filesStillMissing([]string{gone}, roots); got != "" {
		t.Fatalf("gone under a root with files: %q", got)
	}
	if err := os.WriteFile(gone, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := filesStillMissing([]string{gone}, roots); got != "its files are back on disk" {
		t.Fatalf("back: %q", got)
	}
}

// The same torrent in two clients, at the same path, shares its file with
// itself in the other: each names the other by client and hash. Its copy
// in a third client, at a path of its own, shares nothing.
func TestShareGroupAcrossClients(t *testing.T) {
	files := []model.File{file("/dl/a.mkv", 10, 1, 1000), file("/other/a.mkv", 11, 1, 1000)}
	refs := []model.TorrentFileRef{{ServiceID: "q1", Hash: "AAA", Path: "/dl/a.mkv"}, {ServiceID: "q2", Hash: "AAA", Path: "/dl/a.mkv"}, {ServiceID: "q3", Hash: "AAA", Path: "/other/a.mkv"}}
	torrents := []model.Torrent{
		{ServiceID: "q1", Hash: "AAA", AssociationStatus: model.TorrentUnassociated},
		{ServiceID: "q2", Hash: "AAA", AssociationStatus: model.TorrentUnassociated},
		{ServiceID: "q3", Hash: "AAA", AssociationStatus: model.TorrentUnassociated},
	}
	applyTorrentFileEstimates(torrents, files, refs)
	applyTorrentMediaHardlinks(torrents, nil, files, nil, refs)
	want := map[string][]string{"q1": {"q2|aaa"}, "q2": {"q1|aaa"}, "q3": nil}
	for _, tr := range torrents {
		got := tr.ShareKeys()
		if len(got) != len(want[tr.ServiceID]) || (len(got) == 1 && got[0] != want[tr.ServiceID][0]) {
			t.Errorf("%s shares with %v, want %v", tr.ServiceID, got, want[tr.ServiceID])
		}
		if tr.ServiceID != "q3" && (!tr.GroupReclaimableKnown || tr.GroupReclaimableBytes != 1000) {
			t.Errorf("%s: the pair frees the file together: %+v", tr.ServiceID, tr)
		}
	}
}
