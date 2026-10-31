package httpui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
)

type storageData struct {
	Devices      []deviceView
	Capabilities []inventory.RootCapability
	// Kept is the operator's Kept list, newest first.
	Kept []config.KeptItem
	// AutoRemovalDisabled hides the cleanup-candidate/plan section entirely
	// when Removal.AutoMode is Disabled — a disabled setting means
	// automatic removal (and the whole notion of "here's what we'd
	// suggest removing") doesn't exist, not just that nothing gets
	// submitted unattended. The bar/legend/usage numbers above it are
	// unaffected: those describe real disk state, not a removal
	// suggestion.
	AutoRemovalDisabled bool
}

type storageStatsClaim struct {
	Service string `json:"service"`
	Bytes   uint64 `json:"bytes"`
}

type storageStatsDevice struct {
	RepresentativePath   string              `json:"representativePath"`
	Available            bool                `json:"available"`
	Error                string              `json:"error,omitempty"`
	TotalBytes           uint64              `json:"totalBytes"`
	FreeBytes            uint64              `json:"freeBytes"`
	UsedBytes            uint64              `json:"usedBytes"`
	UsagePercent         float64             `json:"usagePercent"`
	TargetUsagePercent   float64             `json:"targetUsagePercent"`
	CriticalUsagePercent float64             `json:"criticalUsagePercent"`
	Claimed              []storageStatsClaim `json:"claimed"`
	UnmanagedBytes       uint64              `json:"unmanagedBytes"`
	OtherBytes           uint64              `json:"otherBytes"`
	ReservedBytes        uint64              `json:"reservedBytes"`
	UsableBytes          uint64              `json:"usableBytes"`
	OvertideUsedBytes    uint64              `json:"overtideUsedBytes"`
	UsagePercentOfUsable float64             `json:"usagePercentOfUsable"`
	CapacityBytes        uint64              `json:"capacityBytes"`
	ElsewhereBytes       uint64              `json:"elsewhereBytes"`
}

// storageStats serves the raw, cheap byte-level numbers a storage device
// exposes every few seconds (see watchStorageChanges in reactive.go) without
// ever touching cleanup.Build — unlike the full Storage page, computing
// which items would actually be removed is real work that has nothing to do
// with disk bytes ticking up or down, and must not be redone on every one of
// those ticks just because the client wants fresher numbers to display.
func (server *Server) storageStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := server.inv.Config()
	devices := server.inv.StorageDevices()
	out := make([]storageStatsDevice, 0, len(devices))
	for _, device := range devices {
		usagePercent := 0.0
		if device.TotalBytes > 0 {
			usagePercent = float64(device.UsedBytes) / float64(device.TotalBytes) * 100
		}
		target, critical := cfg.ThresholdsFor(device.RepresentativePath)
		claimed := make([]storageStatsClaim, 0, len(device.Claimed))
		for _, segment := range device.Claimed {
			claimed = append(claimed, storageStatsClaim{Service: segment.Service, Bytes: segment.Bytes})
		}
		usableBytes := device.UsableBytes()
		overtideUsedBytes := device.OvertideUsedBytes()
		usagePercentOfUsable := 0.0
		if usableBytes > 0 {
			usagePercentOfUsable = float64(overtideUsedBytes) / float64(usableBytes) * 100
		}
		out = append(out, storageStatsDevice{
			RepresentativePath:   device.RepresentativePath,
			Available:            device.Available,
			Error:                device.Error,
			TotalBytes:           device.TotalBytes,
			FreeBytes:            device.FreeBytes,
			UsedBytes:            device.UsedBytes,
			UsagePercent:         usagePercent,
			TargetUsagePercent:   target,
			CriticalUsagePercent: critical,
			Claimed:              claimed,
			UnmanagedBytes:       device.UnmanagedBytes,
			OtherBytes:           device.OtherBytes,
			ReservedBytes:        device.ReservedBytes,
			UsableBytes:          usableBytes,
			OvertideUsedBytes:    overtideUsedBytes,
			UsagePercentOfUsable: usagePercentOfUsable,
			CapacityBytes:        device.CapacityBytes(),
			ElsewhereBytes:       device.UnexplainedOtherBytes(),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type cleanupPlanData struct {
	RepresentativePath string
	RootLabels         []string
	Filesystem         string
	Plan               cleanup.Plan
	// Excluded is the current set of action keys left out of Plan's own
	// selection (see cleanup.Build) — rendered back into the Clean form as
	// hidden fields so submitting Clean, or protecting one more row,
	// reproduces exactly this same filtered view server-side rather than
	// risking drift between what's displayed and what gets acted on.
	Excluded []string
	DryRun   bool
}

// planForDevice rebuilds the named device's current cleanup plan fresh,
// leaving out any action whose key is in excluded (nil/empty for the
// normal case) — shared by the plan window, Clean and Protect (see
// routes_storage.go) so Clean/Protect always act against a live
// recomputation rather than trusting whatever was true when the modal was
// first opened, matching every other removal path's "recompute right
// before acting" convention.
func (server *Server) planForDevice(path string, excluded map[string]bool) (cleanupPlanData, bool) {
	items, _, _ := server.inv.Snapshot()
	projection := server.pendingProjection()
	items = projection.filterMedia(items)
	ts := projection.filterTorrents(server.inv.TorrentSnapshot())
	planningReliable := server.planningReliable(server.inv.ReliabilitySnapshot())
	excludedList := make([]string, 0, len(excluded))
	for key := range excluded {
		excludedList = append(excludedList, key)
	}
	for _, view := range server.deviceViews(items, ts, planningReliable, excluded) {
		if view.Storage.RepresentativePath == path {
			return cleanupPlanData{RepresentativePath: path, RootLabels: view.Storage.RootLabels, Filesystem: view.Storage.Filesystem, Plan: view.Plan, Excluded: excludedList, DryRun: server.inv.Config().Removal.DryRun}, true
		}
	}
	return cleanupPlanData{RepresentativePath: path, Excluded: excludedList}, false
}

// excludedSetFrom builds the excluded-keys set the plan window, Clean and
// Protect all share from a request's "excluded" values — GET query
// parameters (opening/reopening the window) or the JSON body (Clean and
// Protect, which carry the window's current skips forward so they
// recompute the identical filtered plan the user is actually looking at).
func excludedSetFrom(values []string) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]bool, len(values))
	for _, v := range values {
		if v != "" {
			set[v] = true
		}
	}
	return set
}

// shownKey identifies a plan row as it was shown: its action and every
// torrent removed with it. A row whose torrents changed (a cross-seed added
// to its group, say) no longer matches what was shown.
func shownKey(action cleanup.Action) string {
	hashes := make([]string, 0, len(action.Torrents))
	for _, t := range action.Torrents {
		hashes = append(hashes, strings.ToLower(t.Hash))
	}
	sort.Strings(hashes)
	return cleanup.ActionKey(action) + "|" + strings.Join(hashes, ",")
}

// shownActions keeps, of a freshly built plan, only the actions shown when
// Clean was clicked: what wasn't on screen is never removed. gone counts
// the rows shown that the plan no longer has as they were.
func shownActions(actions []cleanup.Action, shown []string) (kept []cleanup.Action, gone int) {
	wanted := map[string]bool{}
	for _, key := range shown {
		wanted[key] = true
	}
	for _, action := range actions {
		key := shownKey(action)
		if wanted[key] {
			kept = append(kept, action)
			delete(wanted, key)
		}
	}
	return kept, len(wanted)
}

// cleanActions queues every given plan row (see queueCleanup) and returns
// one error per row that couldn't be queued.
func (server *Server) cleanActions(actions []cleanup.Action, cfg config.Config) []string {
	var errs []string
	for _, action := range actions {
		if err := server.queueCleanup(action, cfg, "Manual cleanup: "); err != nil {
			errs = append(errs, err.Error())
		}
	}
	return errs
}

// keptMediaName is how a kept title is listed: its title and year.
func keptMediaName(m model.Media) string {
	if m.Year > 0 {
		return fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	return m.Title
}
