package httpui

import (
	"path/filepath"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/store"
)

// A removal still queued behind more than 200 newer events is still
// projected: its torrent stays hidden until the removal is done.
func TestPendingProjectionKeepsQueuedRemovalsPastTheLatest200(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "queued", RequestedKind: "torrent", RequestedKey: "OLDHASH", RequestedLabel: "Old"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		if _, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "success", RequestedKind: "torrent", RequestedKey: "done", RequestedLabel: "Done"}); err != nil {
			t.Fatal(err)
		}
	}
	server, err := New(inventory.New(config.Config{}, database), nil)
	if err != nil {
		t.Fatal(err)
	}
	projection := server.pendingProjection()
	if _, pending := projection.Torrents["oldhash"]; !pending || projection.PendingCount != 1 {
		t.Fatalf("expected the old queued removal to be projected, got count=%d torrents=%v", projection.PendingCount, projection.Torrents)
	}
}
