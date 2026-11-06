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
}

// problemsFresh is how long a reading of the failed removals is reused:
// every page's live status asks for their count, and reading what's left
// copies the whole file and torrent model.
const problemsFresh = 30 * time.Second

// problems are problemsNow, read again at most every problemsFresh (or at
// once after a dismissal).
func (server *Server) problems() []problemJSON {
	server.problemsMu.Lock()
	defer server.problemsMu.Unlock()
	if server.problemsAt.IsZero() || time.Since(server.problemsAt) > problemsFresh {
		// Before the file model is read (or while it can't be), everything
		// would look gone: the last reading stands.
		if problems, ok := server.problemsNow(); ok {
			server.problemsCache, server.problemsAt = problems, time.Now()
		}
	}
	if server.problemsCache == nil {
		return []problemJSON{}
	}
	return server.problemsCache
}

// forgetProblems has the next reading of the failed removals made anew.
func (server *Server) forgetProblems() {
	server.problemsMu.Lock()
	server.problemsAt = time.Time{}
	server.problemsMu.Unlock()
}

// problemsNow are the live removals that didn't go through in full, weren't
// dismissed, and still have something left: once what failed is gone,
// removed again or otherwise, the operation needs nothing more. One whose
// plan wasn't recorded changed nothing (the plan is recorded as it starts),
// so it needs nothing either. ok is false while the file model isn't read.
func (server *Server) problemsNow() ([]problemJSON, bool) {
	out := []problemJSON{}
	if server.inv == nil || server.inv.Store() == nil {
		return out, true
	}
	events, err := server.inv.Store().ProblemHistoryEvents()
	if err != nil {
		return nil, false
	}
	if len(events) == 0 {
		return out, true
	}
	files, mediaRefs, _, updated, err := server.inv.FileSnapshot()
	if err != nil || updated.IsZero() {
		return nil, false
	}
	torrents := map[string]model.Torrent{}
	for _, t := range server.inv.TorrentSnapshot() {
		torrents[t.Hash] = t
	}
	for _, event := range events {
		left, known := leftoversOf(event, files, mediaRefs, torrents)
		if !known || len(left) == 0 {
			continue
		}
		out = append(out, problemJSON{ID: event.ID, CreatedAt: event.CreatedAt, Label: event.RequestedLabel, Status: event.Status, Error: event.Error, Leftovers: left})
	}
	return out, true
}

// leftoversOf reads an operation's recorded plan and keeps what's still
// there of what it selected. known is false when no plan was recorded.
func leftoversOf(event store.HistoryEvent, files []model.File, mediaRefs []model.MediaFileRef, torrents map[string]model.Torrent) (left []leftover, known bool) {
	var payload struct {
		Plan struct {
			Files []removal.FileState `json:"files"`
		} `json:"plan"`
	}
	if json.Unmarshal(event.Payload, &payload) != nil || len(payload.Plan.Files) == 0 {
		return nil, false
	}
	present := map[string]bool{}
	for _, f := range files {
		if f.Exists {
			present[filepath.Clean(f.Path)] = true
		}
	}
	managed := map[string]model.MediaFileRef{}
	for _, ref := range mediaRefs {
		managed[managedFileKey(ref)] = ref
	}
	byTorrent := map[string]*leftover{}
	byTitle := map[string]*leftover{}
	var titles []string
	var unmanaged *leftover
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
			// A title's files, whichever removal selected them: the title
			// is the file's owner as the inventory has it now.
			ref, ok := managed[f.OwnerKey]
			if !ok {
				continue
			}
			key := mediaOperationKey(ref.MediaType, ref.MediaID, ref.ServiceID)
			if byTitle[key] == nil {
				label := f.Label
				if label == "" {
					label = event.RequestedLabel
				}
				byTitle[key] = &leftover{Kind: "media", Label: label, ServiceID: ref.ServiceID, MediaType: string(ref.MediaType), MediaID: strconv.Itoa(ref.MediaID)}
				titles = append(titles, key)
			}
			byTitle[key].Bytes += f.SizeBytes
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
	for _, key := range titles {
		left = append(left, *byTitle[key])
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

// historyProblems answers GET /api/history/problems, read anew: the page
// is what someone looks at after removing what was left.
func (server *Server) historyProblems(w http.ResponseWriter, r *http.Request) {
	server.forgetProblems()
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
	server.forgetProblems()
	server.publishUIChange("history-dismissed")
	done(w)
}
