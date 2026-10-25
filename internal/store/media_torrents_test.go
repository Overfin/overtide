package store

import (
	"path/filepath"
	"testing"

	"github.com/overfin/overtide/internal/model"
)

// What qBittorrent sent survives a save: its incremental sync won't send it
// again.
func TestSaveTorrentsKeepsWhatQBittorrentSent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveTorrents([]model.Torrent{{Client: "qb", Hash: "abc", Private: true, SeedingTime: 86400, AmountLeftBytes: 5, Tracker: "https://tracker.example.org"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.LoadTorrents()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || !loaded[0].Private || loaded[0].SeedingTime != 86400 || loaded[0].AmountLeftBytes != 5 || loaded[0].Tracker != "https://tracker.example.org" {
		t.Fatalf("loaded: %#v", loaded)
	}
}
