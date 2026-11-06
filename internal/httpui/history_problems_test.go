package httpui

import (
	"testing"

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
	mediaRefs := []model.MediaFileRef{{Source: "sonarr", SourceFileID: 21929}}
	if left, _ := leftoversOf(event, nil, mediaRefs, nil); len(left) != 1 || left[0].Kind != "media" || left[0].MediaID != "1658" {
		t.Fatalf("episodes still there: %+v", left)
	}
	if _, known := leftoversOf(store.HistoryEvent{Payload: []byte(`{}`)}, nil, nil, nil); known {
		t.Fatal("no plan, yet known")
	}
}
