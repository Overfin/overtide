package httpui

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/pacing"
	"github.com/overfin/overtide/internal/tasks"
)

// Adding's tasks, as main registers them.
const (
	addingOverfinTaskID = "adding-overfin"
	addingTaskID        = "adding"
)

// SetAdding gives the pages adding and the trackers' pace. Without them,
// adding's routes answer 503.
func (server *Server) SetAdding(adder *adding.Service, pacer *pacing.Pacer) {
	server.adder, server.pacer = adder, pacer
}

// addingReady wraps an adding route: without adding, it answers 503.
func (server *Server) addingReady(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if server.adder == nil || server.pacer == nil || server.inv == nil {
			fail(w, http.StatusServiceUnavailable, "Adding is unavailable.")
			return
		}
		handler(w, r)
	}
}

// addingTasks is where adding's tasks stand.
func (server *Server) addingTasks() []tasks.Status {
	if server.tasks == nil {
		return nil
	}
	ids := []string{addingOverfinTaskID, addingTaskID}
	return slices.DeleteFunc(server.tasks.Snapshot(), func(status tasks.Status) bool { return !slices.Contains(ids, status.ID) })
}

// runAdding has adding's tasks run as soon as they can, in this order.
func (server *Server) runAdding(ids ...string) error {
	if server.tasks == nil {
		return fmt.Errorf("the scheduler is unavailable")
	}
	for _, id := range ids {
		// Accepted manual triggers are durable (Scheduler-Spec 3.2): a
		// restart doesn't lose what was asked.
		if _, err := server.tasks.Submit(tasks.Request{TaskID: id, Kind: tasks.TriggerManual, Priority: tasks.PriorityManual, Durable: true, Cause: "Asked on the Adding page"}); err != nil {
			return err
		}
	}
	return nil
}

func addingTypeLabel(kind string) string {
	for _, known := range allServiceTypes {
		if known.Value == kind {
			return known.Label
		}
	}
	return kind
}

// addingSettingsData is the Settings page's adding section: the settings,
// what the last "Manage all" pass did, and where titles go.
type addingSettingsData struct {
	Mode           string                `json:"mode"`
	Manage         string                `json:"manage"`
	Searches       int                   `json:"searches"`
	TitlesPerDay   int                   `json:"titlesPerDay"`
	UpgradesPerDay int                   `json:"upgradesPerDay"`
	Summary        *adding.ManageSummary `json:"summary,omitempty"`
	Destinations   []destinationRow      `json:"destinations"`
}

// addingSettings answers GET /api/settings/adding.
func (server *Server) addingSettings(w http.ResponseWriter, r *http.Request) {
	cfg := server.inv.Config()
	data := addingSettingsData{Mode: cfg.Adding.Mode, Manage: cfg.Adding.Manage, Searches: cfg.Adding.SearchesPerRun(), TitlesPerDay: cfg.Adding.PerDay(), UpgradesPerDay: cfg.Adding.UpgradesPerDay,
		Destinations: server.destinationRows(r.Context())}
	if data.Mode == "" {
		data.Mode = config.AddingOff
	}
	if data.Manage == "" {
		data.Manage = config.ManageOwn
	}
	if data.Manage == config.ManageAll {
		if summary, err := server.adder.Summary(); err == nil && !summary.At.IsZero() {
			data.Summary = &summary
		}
	}
	writeJSON(w, http.StatusOK, data)
}

type addingForm struct {
	Mode           string `json:"mode"`
	Manage         string `json:"manage"`
	Searches       int    `json:"searches"`
	TitlesPerDay   int    `json:"titlesPerDay"`
	UpgradesPerDay int    `json:"upgradesPerDay"`
}

// addingSettingsSave saves the adding settings (PUT /api/settings/adding).
func (server *Server) addingSettingsSave(w http.ResponseWriter, r *http.Request) {
	var in addingForm
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.inv.SetAdding(in.Mode, in.Manage, in.Searches, in.TitlesPerDay, in.UpgradesPerDay); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	done(w)
}
