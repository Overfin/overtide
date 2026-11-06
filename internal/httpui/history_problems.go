package httpui

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/removal"
	"github.com/overfin/overtide/internal/store"
)

// leftover is what a live removal that didn't go through in full planned to
// remove and is still there: a torrent, a title's files, or files no service
// claims. Each is removed again through the removal window, its plan made
// anew on what's there now.
type leftover struct {
	// Kind is the removal window's: torrent, media or unmanaged.
	Kind      string   `json:"kind"`
	Label     string   `json:"label"`
	Hash      string   `json:"hash,omitempty"`
	ServiceID string   `json:"serviceId,omitempty"`
	MediaType string   `json:"mediaType,omitempty"`
	MediaID   string   `json:"mediaId,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Bytes     int64    `json:"bytes"`
}

// problemJSON is a removal that didn't go through in full, with what's left
// of it.
type problemJSON struct {
	ID        int64      `json:"id"`
	CreatedAt time.Time  `json:"createdAt,omitzero"`
	Label     string     `json:"label"`
	Status    string     `json:"status"`
	Error     string     `json:"error,omitempty"`
	Leftovers []leftover `json:"leftovers"`
	// Unknown: its plan wasn't recorded, so what's left can't be said.
	Unknown bool `json:"unknown,omitempty"`
}

// problems are the live removals that didn't go through in full, weren't
// dismissed, and still have something left (or can't say): once what
// failed is gone, removed again or otherwise, the operation needs nothing
// more.
func (server *Server) problems() []problemJSON {
	out := []problemJSON{}
	if server.inv == nil || server.inv.Store() == nil {
		return out
	}
	events, err := server.inv.Store().ProblemHistoryEvents()
	if err != nil || len(events) == 0 {
		return out
	}
	files, mediaRefs, _, _, _ := server.inv.FileSnapshot()
	torrents := map[string]model.Torrent{}
	for _, t := range server.inv.TorrentSnapshot() {
		torrents[t.Hash] = t
	}
	for _, event := range events {
		left, known := leftoversOf(event, files, mediaRefs, torrents)
		if known && len(left) == 0 {
			continue
		}
		out = append(out, problemJSON{ID: event.ID, CreatedAt: event.CreatedAt, Label: event.RequestedLabel, Status: event.Status, Error: event.Error, Leftovers: left, Unknown: !known})
	}
	return out
}

// leftoversOf reads an operation's recorded plan and keeps what's still
// there of what it selected. known is false when no plan was recorded.
func leftoversOf(event store.HistoryEvent, files []model.File, mediaRefs []model.MediaFileRef, torrents map[string]model.Torrent) (left []leftover, known bool) {
	var payload struct {
		Command struct {
			Form map[string][]string `json:"form"`
		} `json:"command"`
		Plan struct {
			Files []removal.FileState `json:"files"`
		} `json:"plan"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil || len(payload.Plan.Files) == 0 {
		return nil, false
	}
	form := func(key string) string {
		if values := payload.Command.Form[key]; len(values) > 0 {
			return values[0]
		}
		return ""
	}
	present := map[string]bool{}
	for _, f := range files {
		if f.Exists {
			present[filepath.Clean(f.Path)] = true
		}
	}
	managed := map[string]bool{}
	for _, ref := range mediaRefs {
		managed[managedFileKey(ref)] = true
	}
	byTorrent := map[string]*leftover{}
	var media, unmanaged *leftover
	for _, f := range payload.Plan.Files {
		if !f.Selected {
			continue
		}
		switch f.Owner {
		case removal.TorrentOwner:
			t, ok := torrents[f.OwnerKey]
			if !ok {
				continue
			}
			if byTorrent[f.OwnerKey] == nil {
				byTorrent[f.OwnerKey] = &leftover{Kind: "torrent", Label: t.Name, Hash: t.Hash, ServiceID: t.ServiceID}
			}
			byTorrent[f.OwnerKey].Bytes += f.SizeBytes
		case removal.MediaOwner:
			if !managed[f.OwnerKey] || form("kind") != "media" {
				continue
			}
			if media == nil {
				media = &leftover{Kind: "media", Label: event.RequestedLabel, ServiceID: form("service_id"), MediaType: form("media_type"), MediaID: form("media_id")}
			}
			media.Bytes += f.SizeBytes
		case removal.UnmanagedOwner:
			if !present[filepath.Clean(f.Path)] {
				continue
			}
			if unmanaged == nil {
				unmanaged = &leftover{Kind: "unmanaged", Label: "Files no service claims"}
			}
			unmanaged.Paths = append(unmanaged.Paths, f.Path)
			unmanaged.Bytes += f.SizeBytes
		}
	}
	if media != nil {
		left = append(left, *media)
	}
	hashes := make([]string, 0, len(byTorrent))
	for hash := range byTorrent {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	for _, hash := range hashes {
		left = append(left, *byTorrent[hash])
	}
	if unmanaged != nil {
		left = append(left, *unmanaged)
	}
	return left, true
}

// historyProblems answers GET /api/history/problems.
func (server *Server) historyProblems(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"problems": server.problems()})
}

// historyDismiss answers POST /api/history/{id}/dismiss: the operation
// needs nothing more.
func (server *Server) historyDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || server.inv == nil || server.inv.Store() == nil {
		fail(w, http.StatusNotFound, "No such operation.")
		return
	}
	if err := server.inv.Store().DismissHistoryEvent(id); err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	server.publishUIChange("history-dismissed")
	done(w)
}
