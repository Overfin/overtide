package httpui

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/tasks"
)

// arrEvents answers the events Radarr and Sonarr send (POST /events/arr,
// see adding's events.go), outside the sign-in: they come with the
// webhook's secret instead. A title added starts the adding run at once,
// which takes it; a test event is noted, so Overtide knows the address it
// tried reaches it.
func (server *Server) arrEvents(w http.ResponseWriter, r *http.Request) {
	// Without adding, no secret was given out: nothing gets in.
	secret, err := "", error(nil)
	if server.adder != nil {
		secret, err = server.adder.EventsSecret()
	}
	user, password, ok := r.BasicAuth()
	if err != nil || secret == "" || !ok || user != adding.EventsUser || subtle.ConstantTimeCompare([]byte(password), []byte(secret)) != 1 {
		fail(w, http.StatusUnauthorized, "signed out")
		return
	}
	var event struct {
		EventType string `json:"eventType"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&event); err != nil {
		fail(w, http.StatusBadRequest, "not an event")
		return
	}
	switch event.EventType {
	case "Test":
		server.adder.HeardTest()
	case "MovieAdded", "SeriesAdd":
		if server.tasks != nil {
			if _, err := server.tasks.Submit(tasks.Request{TaskID: addingTaskID, Kind: tasks.TriggerEvent, Priority: tasks.PriorityManual, Cause: "A title was added in Radarr or Sonarr"}); err != nil {
				log.Printf("[adding] a title added: the run couldn't start: %v", err)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
