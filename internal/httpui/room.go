package httpui

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

// roomPath is where other apps ask whether a folder has room (Overswell,
// before adding a title): with Overtide's API key in X-Api-Key, not a
// session.
const roomPath = "/api/v1/room"

type roomAnswer struct {
	// State is "ok", "critical" (adding stops) or "unknown".
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// room answers GET /api/v1/room?folder=<path>.
func (server *Server) room(w http.ResponseWriter, r *http.Request) {
	if server.inv == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	key := server.inv.Config().APIKey
	if key == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Api-Key")), []byte(key)) != 1 {
		http.Error(w, "a valid X-Api-Key is required", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	folder := r.URL.Query().Get("folder")
	if folder == "" {
		http.Error(w, "folder is required", http.StatusBadRequest)
		return
	}
	state, reason := server.inv.Room(folder)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(roomAnswer{State: state, Reason: reason})
}
