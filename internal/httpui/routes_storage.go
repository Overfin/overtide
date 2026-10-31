package httpui

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// storageRoutes are the Storage page's JSON API: the devices with their
// cleanup plans, each device's settings, the plan's rows (skipped, kept,
// cleaned), the Kept list, and the cheap byte totals live updates ask for.
func (server *Server) storageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/storage", server.apiStorage)
	mux.HandleFunc("GET /api/storage/stats", server.storageStats)
	mux.HandleFunc("GET /api/storage/device", server.apiDeviceSettings)
	mux.HandleFunc("PUT /api/storage/device", server.apiSetDeviceSettings)
	mux.HandleFunc("GET /api/storage/plan", server.apiCleanupPlan)
	mux.HandleFunc("POST /api/storage/plan/clean", server.apiCleanupPlanClean)
	mux.HandleFunc("POST /api/storage/plan/protect", server.apiCleanupPlanProtect)
	mux.HandleFunc("POST /api/storage/keep", server.apiKeep)
}

// storageClaimJSON is one service's share of a device, with that device's
// own roots of the service (never another device's).
type storageClaimJSON struct {
	Service string   `json:"service"`
	Bytes   uint64   `json:"bytes"`
	Roots   []string `json:"roots"`
}

// storagePlanJSON is a device's cleanup plan as its card shows it: the
// aggregate only; the rows are the plan window's (GET /api/storage/plan).
type storagePlanJSON struct {
	TargetUsagePercent   float64 `json:"targetUsagePercent"`
	CriticalUsagePercent float64 `json:"criticalUsagePercent"`
	NeedBytes            uint64  `json:"needBytes"`
	SelectedBytes        uint64  `json:"selectedBytes"`
	Message              string  `json:"message"`
	Actions              int     `json:"actions"`
}

// storageDeviceJSON is one physical storage device: its identity, its
// bytes, and (on the Storage page) its plan.
type storageDeviceJSON struct {
	Path              string             `json:"path"`
	Name              string             `json:"name"`
	RootLabels        []string           `json:"rootLabels"`
	Filesystem        string             `json:"filesystem"`
	Available         bool               `json:"available"`
	Error             string             `json:"error,omitempty"`
	TotalBytes        uint64             `json:"totalBytes"`
	FreeBytes         uint64             `json:"freeBytes"`
	UnmanagedBytes    uint64             `json:"unmanagedBytes"`
	ReservedBytes     uint64             `json:"reservedBytes"`
	UsableBytes       uint64             `json:"usableBytes"`
	OvertideUsedBytes uint64             `json:"overtideUsedBytes"`
	CapacityBytes     uint64             `json:"capacityBytes"`
	ElsewhereBytes    uint64             `json:"elsewhereBytes"`
	Claimed           []storageClaimJSON `json:"claimed"`
	// Enabled is false when automatic removal is off for this device.
	Enabled bool             `json:"enabled"`
	Plan    *storagePlanJSON `json:"plan,omitempty"`
	PlanErr string           `json:"planError,omitempty"`
}

func deviceJSON(view deviceView, withPlan bool) storageDeviceJSON {
	device := view.Storage
	out := storageDeviceJSON{
		Path: device.RepresentativePath, Name: view.Name, RootLabels: device.RootLabels, Filesystem: device.Filesystem,
		Available: device.Available, Error: device.Error, TotalBytes: device.TotalBytes, FreeBytes: device.FreeBytes,
		UnmanagedBytes: device.UnmanagedBytes, ReservedBytes: device.ReservedBytes, Claimed: []storageClaimJSON{}, Enabled: view.Enabled,
	}
	if out.RootLabels == nil {
		out.RootLabels = []string{}
	}
	if device.Available {
		out.UsableBytes, out.OvertideUsedBytes = device.UsableBytes(), device.OvertideUsedBytes()
		out.CapacityBytes, out.ElsewhereBytes = device.CapacityBytes(), device.UnexplainedOtherBytes()
	}
	for _, segment := range device.Claimed {
		roots := []string{}
		for _, root := range device.ServiceRoots[segment.Service] {
			roots = append(roots, shortPath(root))
		}
		out.Claimed = append(out.Claimed, storageClaimJSON{Service: segment.Service, Bytes: segment.Bytes, Roots: roots})
	}
	if withPlan {
		p := view.Plan
		out.Plan = &storagePlanJSON{TargetUsagePercent: p.TargetUsagePercent, CriticalUsagePercent: p.CriticalUsagePercent,
			NeedBytes: p.NeedBytes, SelectedBytes: p.SelectedBytes, Message: p.Message, Actions: len(p.Actions)}
		if view.PlanErr != nil {
			out.PlanErr = view.PlanErr.Error()
		}
	}
	return out
}

type capabilityJSON struct {
	Path                  string `json:"path"`
	ServiceName           string `json:"serviceName"`
	ServiceType           string `json:"serviceType"`
	Purpose               string `json:"purpose,omitempty"`
	Reachable             bool   `json:"reachable"`
	Filesystem            string `json:"filesystem,omitempty"`
	FreeBytes             uint64 `json:"freeBytes"`
	ReservedBytes         uint64 `json:"reservedBytes"`
	SelfReportedCount     int    `json:"selfReportedCount"`
	SelfReportedSizeBytes int64  `json:"selfReportedSizeBytes"`
}

type keptJSON struct {
	Key    string    `json:"key"`
	Name   string    `json:"name"`
	From   string    `json:"from,omitempty"`
	KeptAt time.Time `json:"keptAt,omitzero"`
}

type storageJSON struct {
	Devices      []storageDeviceJSON `json:"devices"`
	Capabilities []capabilityJSON    `json:"capabilities"`
	Kept         []keptJSON          `json:"kept"`
	// AutoRemovalDisabled: no suggestion at all (removal.auto_mode off).
	AutoRemovalDisabled bool `json:"autoRemovalDisabled"`
}

// storageJSONFrom turns the Storage page's data into its answer.
func storageJSONFrom(data storageData) storageJSON {
	out := storageJSON{Devices: []storageDeviceJSON{}, Capabilities: []capabilityJSON{}, Kept: []keptJSON{}, AutoRemovalDisabled: data.AutoRemovalDisabled}
	for _, view := range data.Devices {
		out.Devices = append(out.Devices, deviceJSON(view, true))
	}
	for _, c := range data.Capabilities {
		out.Capabilities = append(out.Capabilities, capabilityJSON{Path: c.Path, ServiceName: c.ServiceName, ServiceType: c.ServiceType, Purpose: c.Purpose,
			Reachable: c.Reachable, Filesystem: c.Filesystem, FreeBytes: c.FreeBytes, ReservedBytes: c.ReservedBytes,
			SelfReportedCount: c.SelfReportedCount, SelfReportedSizeBytes: c.SelfReportedSizeBytes})
	}
	for _, k := range data.Kept {
		out.Kept = append(out.Kept, keptJSON{Key: k.Key, Name: k.Name, From: k.From, KeptAt: k.KeptAt})
	}
	return out
}

// storageSnapshot is what the Storage page shows: every device with its
// plan, the services' roots, and the Kept list (titles tagged to keep
// first, by name, then those kept here, newest first).
func (server *Server) storageSnapshot() storageData {
	items, _, _ := server.inv.Snapshot()
	var keptTitles []config.KeptItem
	for _, m := range items {
		if model.HasTag(m.Tags, model.TagKeep) {
			keptTitles = append(keptTitles, config.KeptItem{Key: config.KeptMediaKey(string(m.Type), m.ServiceID, m.SourceID), Name: keptMediaName(m), From: "tag " + model.TagKeep})
		}
	}
	sort.Slice(keptTitles, func(i, j int) bool { return strings.ToLower(keptTitles[i].Name) < strings.ToLower(keptTitles[j].Name) })
	projection := server.pendingProjection()
	items = projection.filterMedia(items)
	ts := projection.filterTorrents(server.inv.TorrentSnapshot())
	planningReliable := server.planningReliable(server.inv.ReliabilitySnapshot())
	cfg := server.inv.Config()
	autoMode := cfg.Removal.AutoMode
	data := storageData{
		Devices:             server.deviceViews(items, ts, planningReliable, nil),
		Capabilities:        server.inv.StorageCapabilities(),
		AutoRemovalDisabled: autoMode != config.RemovalAutoConfirm && autoMode != config.RemovalAutoAuto,
	}
	data.Kept = keptTitles
	for i := len(cfg.Kept) - 1; i >= 0; i-- {
		data.Kept = append(data.Kept, cfg.Kept[i])
	}
	return data
}

func (server *Server) apiStorage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, storageJSONFrom(server.storageSnapshot()))
}

type deviceSettingsJSON struct {
	Path                    string   `json:"path"`
	RootLabels              []string `json:"rootLabels"`
	Filesystem              string   `json:"filesystem"`
	Name                    string   `json:"name"`
	TargetUsagePercent      float64  `json:"targetUsagePercent"`
	CriticalUsagePercent    float64  `json:"criticalUsagePercent"`
	AutomaticRemovalEnabled bool     `json:"automaticRemovalEnabled"`
}

// apiDeviceSettings is a device's settings window: its whole identity
// (every root grouped under it, not one path) and its thresholds.
func (server *Server) apiDeviceSettings(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	cfg := server.inv.Config()
	target, critical := cfg.ThresholdsFor(path)
	out := deviceSettingsJSON{Path: path, RootLabels: []string{}, Name: cfg.DeviceName(path), TargetUsagePercent: target, CriticalUsagePercent: critical,
		AutomaticRemovalEnabled: cfg.AutomaticRemovalEnabledFor(path)}
	for _, device := range server.inv.StorageDevices() {
		if device.RepresentativePath == path {
			if device.RootLabels != nil {
				out.RootLabels = device.RootLabels
			}
			out.Filesystem = device.Filesystem
			break
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// apiSetDeviceSettings saves a device's name and thresholds, and whether
// automatic removal looks at it at all.
func (server *Server) apiSetDeviceSettings(w http.ResponseWriter, r *http.Request) {
	var in deviceSettingsJSON
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.inv.SetDeviceThreshold(in.Path, in.Name, in.TargetUsagePercent, in.CriticalUsagePercent, in.AutomaticRemovalEnabled); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	done(w)
}

// cleanupRowJSON is one row of a cleanup plan, as the window lists it.
type cleanupRowJSON struct {
	// Key re-finds the action in a plan built again (skip, keep); Shown is
	// what Clean sends back: the action and every torrent removed with it.
	Key   string `json:"key"`
	Shown string `json:"shown"`
	Title string `json:"title"`
	Meta  string `json:"meta"`
	// With lists what goes with it: cross-seeds, torrents removed with a
	// title.
	With string `json:"with,omitempty"`
}

type cleanupPlanJSON struct {
	Path          string           `json:"path"`
	RootLabels    []string         `json:"rootLabels"`
	Filesystem    string           `json:"filesystem"`
	DryRun        bool             `json:"dryRun"`
	Message       string           `json:"message"`
	SelectedBytes uint64           `json:"selectedBytes"`
	Excluded      []string         `json:"excluded"`
	Torrents      []cleanupRowJSON `json:"torrents"`
	Media         []cleanupRowJSON `json:"media"`
}

// cleanupRow says one plan action the way the window shows it.
func cleanupRow(action cleanup.Action) cleanupRowJSON {
	row := cleanupRowJSON{Key: cleanup.ActionKey(action), Shown: shownKey(action)}
	size := cleanup.Human(uint64(max(action.ReclaimableBytes, 0)))
	if action.Kind == cleanup.StandaloneTorrent {
		first := action.Torrents[0]
		row.Title = first.Name
		row.Meta = torrentActionContext(first) + " · " + size
		if len(action.Torrents) > 1 {
			names := make([]string, 0, len(action.Torrents)-1)
			for _, t := range action.Torrents[1:] {
				names = append(names, t.Name)
			}
			row.With = "With its cross-seeds, removed together: " + strings.Join(names, ", ")
		}
		return row
	}
	switch {
	case action.Kind == cleanup.HardlinkedBundle:
		row.Title = action.Media.Title
		if action.Season != nil {
			row.Title += fmt.Sprintf(" · Season %d", action.Season.Number)
		}
		row.Title += fmt.Sprintf(" + %d hardlinked torrent(s)", len(action.Torrents))
	case action.Kind == cleanup.StandaloneSeason && action.Season != nil:
		row.Title = fmt.Sprintf("%s · Season %d", action.Media.Title, action.Season.Number)
	default:
		row.Title = action.Media.Title
	}
	row.Meta = size
	if action.Media.ServiceName != "" {
		row.Meta = action.Media.ServiceName + " · " + size
	}
	if len(action.Torrents) > 0 {
		names := make([]string, 0, len(action.Torrents))
		for _, t := range action.Torrents {
			names = append(names, t.Name)
		}
		row.With = "Torrents removed with it: " + strings.Join(names, ", ")
	}
	return row
}

func cleanupPlanJSONFrom(data cleanupPlanData) cleanupPlanJSON {
	out := cleanupPlanJSON{Path: data.RepresentativePath, RootLabels: data.RootLabels, Filesystem: data.Filesystem, DryRun: data.DryRun,
		Message: data.Plan.Message, SelectedBytes: data.Plan.SelectedBytes, Excluded: data.Excluded, Torrents: []cleanupRowJSON{}, Media: []cleanupRowJSON{}}
	if out.RootLabels == nil {
		out.RootLabels = []string{}
	}
	if out.Excluded == nil {
		out.Excluded = []string{}
	}
	sort.Strings(out.Excluded)
	for _, action := range torrentCleanupActions(data.Plan.Actions) {
		out.Torrents = append(out.Torrents, cleanupRow(action))
	}
	for _, action := range mediaCleanupActions(data.Plan.Actions) {
		out.Media = append(out.Media, cleanupRow(action))
	}
	return out
}

// apiCleanupPlan is a device's cleanup plan, built fresh, with the actions
// skipped in the window (excluded=…) left out: a replacement takes their
// place. A device no longer known answers the empty plan.
func (server *Server) apiCleanupPlan(w http.ResponseWriter, r *http.Request) {
	data, _ := server.planForDevice(r.URL.Query().Get("path"), excludedSetFrom(r.URL.Query()["excluded"]))
	writeJSON(w, http.StatusOK, cleanupPlanJSONFrom(data))
}

type cleanupPlanRequest struct {
	Path     string   `json:"path"`
	Excluded []string `json:"excluded"`
	// Shown: the rows the window showed, for Clean; Key: the one row kept,
	// for Protect.
	Shown []string `json:"shown"`
	Key   string   `json:"key"`
}

// apiCleanupPlanClean removes the rows the window showed, regardless of
// removal.auto_mode — the same standing as removing any single item by
// hand, just batched. The plan is built again with the window's skips left
// out, and only the rows it showed are acted on (shownActions): anything
// that changed since is left alone and reported. Auto mode's
// unattended-only gates don't apply (a person reviewing the list and
// clicking Clean is that judgment); removal.dry_run and the
// exclude-from-import-lists setting still do (queueCleanup).
func (server *Server) apiCleanupPlanClean(w http.ResponseWriter, r *http.Request) {
	var in cleanupPlanRequest
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	data, found := server.planForDevice(in.Path, excludedSetFrom(in.Excluded))
	if !found {
		fail(w, http.StatusNotFound, "This device is no longer known.")
		return
	}
	actions, gone := shownActions(data.Plan.Actions, in.Shown)
	errs := server.cleanActions(actions, server.inv.Config())
	if gone > 0 {
		errs = append(errs, fmt.Sprintf("%d item(s) changed since the plan was opened and were left alone; reopen the plan to see them", gone))
	}
	if len(errs) > 0 {
		fail(w, http.StatusConflict, strings.Join(errs, "; "))
		return
	}
	done(w)
}

// apiCleanupPlanProtect keeps one row's title or torrent (inventory.Keep):
// on the Kept list until taken off it. It's found in the plan rebuilt with
// the window's same skips, since a fresh unskipped rebuild could have
// chosen a different replacement than the row on screen.
func (server *Server) apiCleanupPlanProtect(w http.ResponseWriter, r *http.Request) {
	var in cleanupPlanRequest
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	data, found := server.planForDevice(in.Path, excludedSetFrom(in.Excluded))
	if !found {
		fail(w, http.StatusNotFound, "This device is no longer known.")
		return
	}
	var target *cleanup.Action
	for i := range data.Plan.Actions {
		if cleanup.ActionKey(data.Plan.Actions[i]) == in.Key {
			target = &data.Plan.Actions[i]
			break
		}
	}
	if target == nil {
		fail(w, http.StatusConflict, "This item is no longer in the plan: reopen it.")
		return
	}
	var err error
	switch {
	case target.Kind == cleanup.StandaloneTorrent:
		err = server.inv.Keep(config.KeptTorrentKey(target.Torrents[0].Hash), target.Torrents[0].Name)
	case target.Media.Type == model.Movie || target.Media.Type == model.Series:
		err = server.inv.Keep(config.KeptMediaKey(string(target.Media.Type), target.Media.ServiceID, target.Media.SourceID), keptMediaName(target.Media))
	default:
		fail(w, http.StatusBadRequest, "This kind of item can't be kept.")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	done(w)
}

type keepRequest struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Unkeep takes it off the Kept list.
	Unkeep bool `json:"unkeep"`
}

// apiKeep keeps a title or a torrent (its Kept key), or takes it off the
// Kept list. Any page may use it.
func (server *Server) apiKeep(w http.ResponseWriter, r *http.Request) {
	var in keepRequest
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		fail(w, http.StatusBadRequest, "key is required")
		return
	}
	var err error
	if in.Unkeep {
		err = server.inv.Unkeep(key)
	} else {
		err = server.inv.Keep(key, strings.TrimSpace(in.Name))
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	done(w)
}
