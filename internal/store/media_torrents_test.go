package store

import (
	"path/filepath"
	"testing"

	"github.com/overfin/overtide/internal/model"
)

// A torrent's private flag and seeding time survive a save: protections
// depend on them, and qBittorrent's incremental sync won't send them again.
func TestSaveTorrentsKeepsPrivateAndSeedingTime(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SaveTorrents([]model.Torrent{{Client: "qb", Hash: "abc", Private: true, SeedingTime: 86400, Tracker: "https://tracker.example.org"}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := db.LoadTorrents()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || !loaded[0].Private || loaded[0].SeedingTime != 86400 || loaded[0].Tracker != "" {
		t.Fatalf("loaded: %#v", loaded)
	}
}
