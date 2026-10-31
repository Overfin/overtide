package httpui

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
)

// consistencyWorkflowInstanceID identifies the single global workflow
// instance every service add/edit/remove coalesces into (see
// scheduleServiceConsistency) — the "inventory" step fetches from every
// configured service in one pass, not just whichever one triggered it, so
// there's exactly one instance to watch regardless of which service the
// progress window was opened for.
const consistencyWorkflowInstanceID = "inventory-and-files-consistency:global"

// scheduleServiceConsistency runs the same inventory-then-files chain a
// removal triggers, so adding/editing/removing a service discovers its
// storage paths (or reclassifies its files as Unmanaged) right away instead
// of waiting for the next periodic file reconciliation — the whole point of
// moving config editing into the app instead of a static file. The quiet
// period coalesces rapid successive changes (e.g. adding several
// services back to back) into one run rather than piling them up.
func (server *Server) scheduleServiceConsistency(cause string) {
	if server.tasks == nil {
		return
	}
	if _, err := server.tasks.AdvanceWorkflow("inventory-and-files-consistency", "global", 5*time.Second, 2*time.Minute, cause); err != nil {
		log.Printf("[http] schedule inventory/files consistency after %s: %v", cause, err)
	}
}

// serviceTypeOption is one entry in the Add-service window's Type select.
type serviceTypeOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// allServiceTypes is every adapter Overtide currently supports, in the
// order they've always been presented — the default when no category
// narrows the list (e.g. opened from the Services page directly).
var allServiceTypes = []serviceTypeOption{
	{"radarr", "Radarr"},
	{"sonarr", "Sonarr"},
	{"qbittorrent", "qBittorrent"},
	{"overfin", "Overfin"},
}

// serviceCategory groups adapter types by the role they fill, purely for
// presentation — it's never persisted. Opening the window from a
// category-specific empty state (Torrents, Library) narrows the Type
// select to just that category's adapters and changes the heading, so a
// future Transmission/Deluge or Lidarr/Readarr adapter only ever needs
// adding to the relevant category's Types list here, not a new window.
type serviceCategory struct {
	Title string              `json:"title"`
	Types []serviceTypeOption `json:"types"`
}

var serviceCategories = map[string]serviceCategory{
	"torrentclient": {Title: "Add torrent client", Types: []serviceTypeOption{
		{"qbittorrent", "qBittorrent"},
	}},
	"medialibrary": {Title: "Add media library", Types: []serviceTypeOption{
		{"radarr", "Radarr"},
		{"sonarr", "Sonarr"},
	}},
}

func addServiceFormDataFor(category string) serviceCategory {
	if found, ok := serviceCategories[category]; ok {
		return found
	}
	return serviceCategory{Title: "Add service", Types: allServiceTypes}
}

// serviceRow is a configured service as the Services page lists it.
type serviceRow struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	OK        bool      `json:"ok"`
	Message   string    `json:"message,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitzero"`
	// Roots are the storage paths the service's own adapter reported.
	Roots []string `json:"roots"`
}

type servicesData struct {
	Services []serviceRow `json:"services"`
}

// servicesList answers GET /api/services: the configured services, whether
// Overtide can reach them, and their storage roots.
func (server *Server) servicesList(w http.ResponseWriter, r *http.Request) {
	data := servicesData{Services: []serviceRow{}}
	roots := server.inv.ServiceRootPaths()
	for _, svc := range server.inv.StatusSnapshot() {
		if !svc.Configured {
			continue
		}
		row := serviceRow{ID: svc.ID, Name: svc.Name, OK: svc.OK, Message: svc.Message, CheckedAt: svc.CheckedAt, Roots: roots[svc.Name]}
		if row.Roots == nil {
			row.Roots = []string{}
		}
		data.Services = append(data.Services, row)
	}
	writeJSON(w, http.StatusOK, data)
}

// serviceTypes answers GET /api/services/types?category=…: the Add-service
// window's heading and Type choices.
func (server *Server) serviceTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, addServiceFormDataFor(r.URL.Query().Get("category")))
}

// serviceForm is a service as the add and edit windows send it.
type serviceForm struct {
	// Type is taken only for a new service.
	Type                  string `json:"type"`
	Name                  string `json:"name"`
	URL                   string `json:"url"`
	APIKey                string `json:"apiKey"`
	Username              string `json:"username"`
	Password              string `json:"password"`
	AllowAutomaticRemoval bool   `json:"allowAutomaticRemoval"`
}

// testServiceConnection is step 1 of the service setup window: a live
// connection check against the entered URL/credentials with nothing
// persisted and no client activated — fast feedback before the service is
// saved (POST /api/services/test).
func (server *Server) testServiceConnection(w http.ResponseWriter, r *http.Request) {
	var in serviceForm
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	candidate := config.Service{Type: in.Type, Name: in.Name, URL: in.URL, APIKey: in.APIKey, Username: in.Username, Password: in.Password}
	if err := server.inv.TestServiceConnection(r.Context(), candidate); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	done(w)
}

// addService validates and live-activates one new service (POST
// /api/services).
func (server *Server) addService(w http.ResponseWriter, r *http.Request) {
	var in serviceForm
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	candidate := config.Service{Type: in.Type, Name: in.Name, URL: in.URL, APIKey: in.APIKey, Username: in.Username, Password: in.Password, AllowAutomaticRemoval: in.AllowAutomaticRemoval}
	if err := server.inv.AddService(r.Context(), candidate); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	server.scheduleServiceConsistency("Service added: " + candidate.Name)
	done(w)
}

// serviceDetail is a service as the edit window shows it: never its
// password or key, only whether it has one.
type serviceDetail struct {
	ID                    string `json:"id"`
	Type                  string `json:"type"`
	Name                  string `json:"name"`
	URL                   string `json:"url"`
	Username              string `json:"username"`
	HasAPIKey             bool   `json:"hasApiKey"`
	HasPassword           bool   `json:"hasPassword"`
	AllowAutomaticRemoval bool   `json:"allowAutomaticRemoval"`
}

func (server *Server) configuredService(id string) (config.Service, bool) {
	for _, service := range server.inv.Config().Services {
		if service.ID == id {
			return service, true
		}
	}
	return config.Service{}, false
}

// serviceGet answers GET /api/services/{id}: the edit window's values.
func (server *Server) serviceGet(w http.ResponseWriter, r *http.Request) {
	found, ok := server.configuredService(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusNotFound, "service not found")
		return
	}
	writeJSON(w, http.StatusOK, serviceDetail{ID: found.ID, Type: found.Type, Name: found.Name, URL: found.URL, Username: found.Username,
		HasAPIKey: found.APIKey != "", HasPassword: found.Password != "", AllowAutomaticRemoval: found.AllowAutomaticRemoval})
}

// editService saves a service's changes (PUT /api/services/{id}).
func (server *Server) editService(w http.ResponseWriter, r *http.Request) {
	var in serviceForm
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	// The edit window only shows the credential fields relevant to the
	// service's type (API key, or username+password, never both) — the
	// other side must be carried forward from the existing value rather
	// than treated as cleared, or a save with no real intent to touch it
	// would silently wipe a credential set outside the UI (e.g.
	// qBittorrent's optional API key).
	existing, _ := server.configuredService(id)
	updates := config.Service{
		Name:                  in.Name,
		URL:                   in.URL,
		APIKey:                existing.APIKey,
		Username:              existing.Username,
		Password:              existing.Password,
		AllowAutomaticRemoval: in.AllowAutomaticRemoval,
	}
	// The window never shows a saved password or key: left empty, it stays.
	if strings.EqualFold(existing.Type, "qbittorrent") {
		updates.Username = in.Username
		if in.Password != "" {
			updates.Password = in.Password
		}
	} else if in.APIKey != "" {
		updates.APIKey = in.APIKey
	}
	if err := server.inv.EditService(r.Context(), id, updates); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	server.scheduleServiceConsistency("Service edited: " + updates.Name)
	done(w)
}

// removeService lets go of a service entirely (DELETE /api/services/{id}).
func (server *Server) removeService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := server.inv.RemoveService(id); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	server.scheduleServiceConsistency("Service removed: " + id)
	done(w)
}

type consistencyStatusResponse struct {
	State       string `json:"state"`
	CurrentStep int    `json:"currentStep"`
	TotalSteps  int    `json:"totalSteps"`
	StepLabel   string `json:"stepLabel"`
}

// serviceConsistencyStatus backs the setup progress window: the panel
// asks again on every live revision (server.tasks.Changes() already
// publishes one on every workflow step advance) and updates the label in
// place (GET /api/services/consistency).
func (server *Server) serviceConsistencyStatus(w http.ResponseWriter, r *http.Request) {
	response := consistencyStatusResponse{State: "succeeded", TotalSteps: 2}
	if server.tasks != nil {
		for _, status := range server.tasks.WorkflowStatuses() {
			if status.ID != consistencyWorkflowInstanceID {
				continue
			}
			response = consistencyStatusResponse{State: status.State, CurrentStep: status.CurrentStep, TotalSteps: status.TotalSteps, StepLabel: status.StepName}
			break
		}
	}
	writeJSON(w, http.StatusOK, response)
}
