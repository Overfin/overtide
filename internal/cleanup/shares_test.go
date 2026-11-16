package cleanup

import (
	"slices"
	"testing"

	"github.com/overfin/overtide/internal/model"
)

func film(title, path string, bytes int64) Action {
	return Action{Kind: StandaloneMedia, Media: model.Media{Title: title, Path: path}, ReclaimableBytes: bytes}
}

func titles(actions []Action) []string {
	var out []string
	for _, a := range actions {
		if a.Media.Title != "" {
			out = append(out, a.Media.Title)
		} else {
			out = append(out, "torrent")
		}
	}
	return out
}

// Lone torrents first, as without shares; then one title at a time from
// the folder furthest over its share, counted again after each.
func TestByShares(t *testing.T) {
	folders := []Folder{{Path: "/data/Films", Bytes: 100, Share: 60}, {Path: "/data/Series", Bytes: 70, Share: 40}}
	ordered := []Action{
		{Kind: StandaloneTorrent, ReclaimableBytes: 5},
		film("s1", "/data/Series/a", 10), // lowest value first
		film("f1", "/data/Films/x", 25),
		film("s2", "/data/Series/b", 10),
		film("f2", "/data/Films/y", 10),
		film("lost", "/elsewhere/z", 50), // in no folder: never taken
	}
	inFolders(ordered, folders)
	// Films 40 over, Series 30 over: f1 (Films 15 over), then s1 (Series
	// 20 over), s2 (Series 10 over), f2 (Films 5 over): 60 freed.
	got := titles(byShares(ordered, 60, folders))
	if want := []string{"torrent", "f1", "s1", "s2", "f2"}; !slices.Equal(got, want) {
		t.Fatalf("taken %v, want %v", got, want)
	}
	// Stops once no folder is over, though the disk needs more.
	got = titles(byShares(ordered, 1000, folders))
	if want := []string{"torrent", "f1", "s1", "s2", "f2"}; !slices.Equal(got, want) {
		t.Fatalf("taken %v, want %v", got, want)
	}
	// Stops once the disk has what it needs.
	if got := titles(byShares(ordered, 4, folders)); !slices.Equal(got, []string{"torrent"}) {
		t.Fatalf("taken %v", got)
	}
	// A folder under its share gives nothing.
	under := []Folder{{Path: "/data/Films", Bytes: 100, Share: 60}, {Path: "/data/Series", Bytes: 30, Share: 40}}
	if got := titles(byShares(ordered, 1000, under)); slices.Contains(got, "s1") {
		t.Fatalf("took from a folder under its share: %v", got)
	}
}
