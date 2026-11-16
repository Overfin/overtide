package cleanup

import (
	"testing"

	"github.com/overfin/overtide/internal/model"
)

// packFilms are two films hardlinked to one pack: each bundle counts the
// pack's own files (100), and the pack takes the films' files with it.
func packFilms(keptB bool) []model.Media {
	pack := model.Torrent{ServiceID: "qb", Hash: "pack", Name: "Pack", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, ReclaimableKnown: true, ReclaimableBytes: 100}
	film := func(id int, value float64) model.Media {
		return model.Media{Type: model.Movie, ServiceID: "r", SourceID: id, Title: "Film", RetentionValue: value, BundleReclaimableKnown: true, BundleReclaimableBytes: 600, Torrents: []model.Torrent{pack}}
	}
	a, b := film(1, 10), film(2, 20)
	b.Protected = keptB
	return []model.Media{a, b}
}

// Two titles sharing a pack go together, at the more valued one's turn,
// the pack with the first, its own files counted once.
func TestSharedPackGoesWithEveryTitleOnce(t *testing.T) {
	mediaTier, torrentTier := classify(packFilms(false), nil)
	ordered := order(mediaTier, torrentTier)
	taken := takeUntil(ordered, 100)
	if len(taken) != 2 {
		t.Fatalf("taken %d, want both titles: %#v", len(taken), taken)
	}
	var bytes int64
	packs := 0
	for _, action := range taken {
		bytes += action.ReclaimableBytes
		packs += len(action.Torrents)
	}
	if bytes != 1100 || packs != 1 {
		t.Fatalf("freed %d (want 1100), pack removed %d times", bytes, packs)
	}
	// Without the other title (skipped, held), neither goes.
	if taken := takeUntil(ordered[:1], 100); len(taken) != 0 {
		t.Fatalf("one without the other: %#v", taken)
	}
}

// A pack a kept title still seeds is never removed with another title.
func TestSharedPackOfAKeptTitleStays(t *testing.T) {
	mediaTier, _ := classify(packFilms(true), nil)
	if len(mediaTier) != 0 {
		t.Fatalf("offered: %#v", mediaTier)
	}
}

// The same hash in two clients is two torrents: a cross-seed group is
// its own client's, never the other's torrent of that hash.
func TestSameHashInTwoClients(t *testing.T) {
	lone := func(service, hash string, with ...string) model.Torrent {
		return model.Torrent{ServiceID: service, Hash: hash, AssociationStatus: model.TorrentOrphaned, SharesFilesWith: with, GroupReclaimableKnown: true, GroupReclaimableBytes: 10}
	}
	other := lone("qb2", "def")
	other.AssociationStatus = model.TorrentCurrent
	_, torrentTier := classify(nil, []model.Torrent{lone("qb1", "abc", "def"), lone("qb1", "def", "abc"), other})
	if len(torrentTier) != 1 || len(torrentTier[0].Torrents) != 2 {
		t.Fatalf("lone torrents: %#v", torrentTier)
	}
	for _, torrent := range torrentTier[0].Torrents {
		if torrent.ServiceID != "qb1" {
			t.Fatalf("another client's torrent in the group: %#v", torrent)
		}
	}
}
