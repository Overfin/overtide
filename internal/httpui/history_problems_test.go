package httpui

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// As on 2026-11-02: a series' episodes went, its torrent stayed. What's left
// is the torrent alone; once it's gone too, nothing is.
func TestLeftoversOf(t *testing.T) {
	event := store.HistoryEvent{ID: 499, RequestedLabel: "Mid-Century Modern", Status: "partial", Payload: []byte(`{
		"command": {"form": {"kind": ["media"], "media_type": ["series"], "media_id": ["1658"], "service_id": ["sonarr-1"]}},
		"plan": {"files": [
			{"path": "/data/Series/M/S01E01.mkv", "owner": "media", "ownerKey": "sonarr:21929", "selected": true, "sizeBytes": 100},
			{"path": "/data/downloads/M/E01.mkv", "owner": "torrent", "ownerKey": "defef4", "selected": true, "sizeBytes": 100},
			{"path": "/data/downloads/M/E02.mkv", "owner": "torrent", "ownerKey": "defef4", "selected": true, "sizeBytes": 50},
			{"path": "/data/downloads/X/x.mkv", "owner": "torrent", "ownerKey": "kept", "selected": false, "sizeBytes": 10}
		]}}`)}
	torrents := map[string]model.Torrent{
		"defef4": {Hash: "defef4", Name: "Mid.Century.Modern.S01", ServiceID: "qb-1"},
		"kept":   {Hash: "kept", Name: "Kept"},
	}
	left, known := leftoversOf(event, nil, nil, torrents)
	if !known || len(left) != 1 || left[0].Kind != "torrent" || left[0].Hash != "defef4" || left[0].ServiceID != "qb-1" || left[0].Bytes != 150 {
		t.Fatalf("%v %+v", known, left)
	}
	if left, known := leftoversOf(event, nil, nil, map[string]model.Torrent{}); !known || len(left) != 0 {
		t.Fatalf("all gone: %v %+v", known, left)
	}
	mediaRefs := []model.MediaFileRef{{Source: "sonarr", SourceFileID: 21929, MediaType: model.Series, MediaID: 1658, ServiceID: "sonarr-1"}}
	if left, _ := leftoversOf(event, nil, mediaRefs, nil); len(left) != 1 || left[0].Kind != "media" || left[0].MediaID != "1658" || left[0].ServiceID != "sonarr-1" {
		t.Fatalf("episodes still there: %+v", left)
	}
	// A torrent's removal that selected a title's files too: they're left
	// over just the same.
	torrentRemoval := event
	torrentRemoval.Payload = []byte(`{"command": {"form": {"kind": ["torrent"]}}, "plan": {"files": [
		{"path": "/data/Series/M/S01E01.mkv", "owner": "media", "ownerKey": "sonarr:21929", "label": "Mid-Century Modern", "selected": true, "sizeBytes": 100}]}}`)
	if left, _ := leftoversOf(torrentRemoval, nil, mediaRefs, nil); len(left) != 1 || left[0].Kind != "media" || left[0].Label != "Mid-Century Modern" {
		t.Fatalf("a torrent removal's title files: %+v", left)
	}
	if _, known := leftoversOf(store.HistoryEvent{Payload: []byte(`{}`)}, nil, nil, nil); known {
		t.Fatal("no plan, yet known")
	}
}

// Before the file model is read, nothing can be said to be gone: the
// reading isn't made (and a previous one stands).
func TestProblemsWaitForTheFileModel(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "partial", RequestedLabel: "X",
		Payload: []byte(`{"plan":{"files":[{"path":"/t/x.mkv","owner":"torrent","ownerKey":"abc","selected":true,"sizeBytes":1}]}}`)}); err != nil {
		t.Fatal(err)
	}
	server := &Server{inv: inventory.New(config.Config{}, database)}
	if _, ok := server.problemsNow(); ok {
		t.Fatal("a reading made before the file model was read")
	}
	server.problemsCache, server.problemsAt = []problemJSON{{ID: 1}}, time.Now().Add(-time.Hour)
	if got := server.problems(); len(got) != 1 {
		t.Fatalf("the previous reading didn't stand: %+v", got)
	}
}
