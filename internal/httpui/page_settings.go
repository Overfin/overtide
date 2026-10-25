package httpui

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/overfin/overtide/internal/config"
)

type settingsPageData struct {
	Error   string
	Success bool

	RemovalError                   string
	RemovalSuccess                 bool
	AutoMode                       string
	AutoRemoveUnassociatedTorrents bool
	DryRun                         bool
	PrivateMinSeedingDays          int
	AutoUnmonitor                  bool
	AutoExcludeFromImportLists     bool
	AutoRemoveIncompleteTorrents   bool
}

func (server *Server) settingsData() settingsPageData {
	cfg := server.inv.Config()
	autoMode := cfg.Removal.AutoMode
	if autoMode == "" {
		autoMode = config.RemovalAutoDisabled
	}
	return settingsPageData{
		AutoMode:                       autoMode,
		AutoRemoveUnassociatedTorrents: cfg.Removal.AutoRemoveUnassociatedTorrents,
		DryRun:                         cfg.Removal.DryRun,
		PrivateMinSeedingDays:          cfg.Protection.PrivateMinSeedingDays,
		AutoUnmonitor:                  cfg.Removal.AutoUnmonitor,
		AutoExcludeFromImportLists:     cfg.Removal.AutoExcludeFromImportLists,
		AutoRemoveIncompleteTorrents:   cfg.Removal.AutoRemoveIncompleteTorrents,
	}
}

func (server *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_ = renderTemplate(w, server.settingsTpl, server.settingsData())
}

// changePassword rotates the admin account's password and, since a
// credential rotation should actually revoke access, signs out every other
// session before re-establishing one for the browser that made the change.
func (server *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	cfg := server.inv.Config()
	data := server.settingsData()
	if !cfg.VerifyPassword(r.FormValue("current_password")) {
		data.Error = "Current password is incorrect."
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	newPassword := r.FormValue("new_password")
	if newPassword != r.FormValue("confirm") {
		data.Error = "New passwords do not match."
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	if err := server.inv.SetCredentials(cfg.Auth.Username, newPassword); err != nil {
		data.Error = err.Error()
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	if server.inv.Store() != nil {
		_ = server.inv.Store().DeleteAllSessions()
		_ = server.startSession(w, r)
	}
	data = server.settingsData()
	data.Success = true
	_ = renderTemplate(w, server.settingsTpl, data)
}

// setRemovalSettings saves the global automatic-removal switches. Automatic
// removal also still requires each service's own "Allow automatic removal"
// checkbox (see the service edit form) — auto_mode controls whether the
// evaluation task runs at all (Disabled), runs and applies its filters but
// never submits anything (Confirm), or runs and submits (Auto);
// auto_remove_unassociated_torrents/dry_run are unrelated checkboxes that
// only submit when checked, so an absent field means false.
func (server *Server) setRemovalSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	autoMode := r.FormValue("auto_mode")
	autoRemoveUnassociated := r.FormValue("auto_remove_unassociated_torrents") == "on"
	dryRun := r.FormValue("dry_run") == "on"
	autoUnmonitor := r.FormValue("auto_unmonitor") == "on"
	autoExcludeFromImportLists := r.FormValue("auto_exclude_from_import_lists") == "on"
	autoRemoveIncomplete := r.FormValue("auto_remove_incomplete_torrents") == "on"
	data := server.settingsData()
	// An empty field means 0, off.
	privateMinSeedingDays := 0
	if raw := strings.TrimSpace(r.FormValue("private_min_seeding_days")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			data.RemovalError = "Days of seeding for private torrents must be a whole number"
			_ = renderTemplate(w, server.settingsTpl, data)
			return
		}
		privateMinSeedingDays = parsed
	}
	if err := server.inv.SetRemovalSettings(autoMode, autoRemoveUnassociated, dryRun, privateMinSeedingDays, autoUnmonitor, autoExcludeFromImportLists, autoRemoveIncomplete); err != nil {
		data.RemovalError = err.Error()
		_ = renderTemplate(w, server.settingsTpl, data)
		return
	}
	data = server.settingsData()
	data.RemovalSuccess = true
	_ = renderTemplate(w, server.settingsTpl, data)
}
