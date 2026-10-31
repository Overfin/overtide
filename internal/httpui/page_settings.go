package httpui

import (
	"net/http"

	"github.com/overfin/overtide/internal/config"
)

// settingsData is what the Settings page shows: the automatic-removal
// switches and the API key other apps ask Overtide with.
type settingsData struct {
	AutoMode                       string `json:"autoMode"`
	AutoRemoveUnassociatedTorrents bool   `json:"autoRemoveUnassociatedTorrents"`
	DryRun                         bool   `json:"dryRun"`
	PrivateMinSeedingDays          int    `json:"privateMinSeedingDays"`
	AutoExcludeFromImportLists     bool   `json:"autoExcludeFromImportLists"`
	AutoRemoveIncompleteTorrents   bool   `json:"autoRemoveIncompleteTorrents"`
	CopiesWithTitles               bool   `json:"copiesWithTitles"`
	// APIKey is what other apps give to ask Overtide something.
	APIKey string `json:"apiKey"`
}

func (server *Server) settingsData() settingsData {
	cfg := server.inv.Config()
	autoMode := cfg.Removal.AutoMode
	if autoMode == "" {
		autoMode = config.RemovalAutoDisabled
	}
	return settingsData{
		AutoMode:                       autoMode,
		AutoRemoveUnassociatedTorrents: cfg.Removal.AutoRemoveUnassociatedTorrents,
		DryRun:                         cfg.Removal.DryRun,
		PrivateMinSeedingDays:          cfg.Protection.PrivateMinSeedingDays,
		AutoExcludeFromImportLists:     cfg.Removal.AutoExcludeFromImportLists,
		AutoRemoveIncompleteTorrents:   cfg.Removal.AutoRemoveIncompleteTorrents,
		CopiesWithTitles:               cfg.Removal.CopiesWithTitles,
		APIKey:                         cfg.APIKey,
	}
}

// settings answers GET /api/settings.
func (server *Server) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, server.settingsData())
}

type passwordForm struct {
	Current string `json:"current"`
	New     string `json:"new"`
	Confirm string `json:"confirm"`
}

// changePassword rotates the admin account's password and, since a
// credential rotation should actually revoke access, signs out every other
// session before re-establishing one for the browser that made the change
// (PUT /api/settings/password).
func (server *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in passwordForm
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := server.inv.Config()
	if !cfg.VerifyPassword(in.Current) {
		fail(w, http.StatusBadRequest, "Current password is incorrect.")
		return
	}
	if in.New != in.Confirm {
		fail(w, http.StatusBadRequest, "New passwords do not match.")
		return
	}
	if err := server.inv.SetCredentials(cfg.Auth.Username, in.New); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if server.inv.Store() != nil {
		// The point of a new password is that other browsers lose access:
		// if that fails, say so rather than report success.
		if err := server.inv.Store().DeleteAllSessions(); err != nil {
			fail(w, http.StatusInternalServerError, "The password changed, but other browsers couldn't be signed out: "+err.Error())
			return
		}
		_ = server.startSession(w, r)
	}
	done(w)
}

// removalForm is the automatic-removal switches as the Settings page sends
// them: every one, each time.
type removalForm struct {
	AutoMode                       string `json:"autoMode"`
	AutoRemoveUnassociatedTorrents bool   `json:"autoRemoveUnassociatedTorrents"`
	DryRun                         bool   `json:"dryRun"`
	AutoExcludeFromImportLists     bool   `json:"autoExcludeFromImportLists"`
	AutoRemoveIncompleteTorrents   bool   `json:"autoRemoveIncompleteTorrents"`
	CopiesWithTitles               bool   `json:"copiesWithTitles"`
	// PrivateMinSeedingDays: 0 is off.
	PrivateMinSeedingDays int `json:"privateMinSeedingDays"`
}

// setRemovalSettings saves the global automatic-removal switches (PUT
// /api/settings/removal). Automatic removal also still requires each
// service's own "Allow automatic removal" (see the service edit window) —
// auto mode controls whether the evaluation task runs at all (Disabled),
// runs and applies its filters but never submits anything (Confirm), or
// runs and submits (Auto).
func (server *Server) setRemovalSettings(w http.ResponseWriter, r *http.Request) {
	var in removalForm
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	removal := config.RemovalConfig{
		AutoMode:                       in.AutoMode,
		AutoRemoveUnassociatedTorrents: in.AutoRemoveUnassociatedTorrents,
		DryRun:                         in.DryRun,
		AutoExcludeFromImportLists:     in.AutoExcludeFromImportLists,
		AutoRemoveIncompleteTorrents:   in.AutoRemoveIncompleteTorrents,
		CopiesWithTitles:               in.CopiesWithTitles,
	}
	if err := server.inv.SetRemovalSettings(removal, in.PrivateMinSeedingDays); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, server.settingsData())
}
