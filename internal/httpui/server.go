package httpui

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/product"
	"github.com/overfin/overtide/internal/tasks"
)

type Server struct {
	inv          *inventory.Service
	tasks        *tasks.Manager
	revisions    *revisionHub
	startOnce    sync.Once
	admissionMu  sync.Mutex
	homeMu       sync.Mutex
	homeRevision uint64
	homeCache    homeData
	// sseMaxLifetime bounds how long any single /ui/events connection is
	// kept open before the server ends it and lets the client's built-in
	// EventSource auto-reconnect start a fresh one — see uiEvents. Zero
	// value in New() is replaced with the real default; tests override it
	// directly to verify the rotation fires without waiting minutes.
	sseMaxLifetime time.Duration
	// sseHeartbeat is how often an open stream sends a keepalive and checks
	// its session is still valid; tests shorten it.
	sseHeartbeat time.Duration
	loginLimiter *loginLimiter
}

func New(inventoryService *inventory.Service, taskManager *tasks.Manager) (*Server, error) {
	server := &Server{inv: inventoryService, tasks: taskManager, revisions: newRevisionHub(), sseMaxLifetime: 3 * time.Minute, sseHeartbeat: 15 * time.Second, loginLimiter: newLoginLimiter()}
	if taskManager != nil {
		if err := taskManager.Register(tasks.Definition{ID: removalTaskID, Name: "Removal operations", Description: "Execute durable owner and filesystem mutations.", PayloadRunner: server.runScheduledRemoval, Resources: []tasks.ResourceClaim{{Resource: "owner-filesystem-mutation", Mode: tasks.ClaimExclusive}}, Priority: tasks.PriorityMutation, Recovery: tasks.RecoveryAttention}); err != nil {
			return nil, err
		}
		if err := taskManager.Register(tasks.Definition{ID: autoRemovalTaskID, Name: "Automatic removal", Description: "Evaluate cross-domain cleanup plans and submit removals for opted-in services.", Interval: autoRemovalEvalInterval, Runner: server.runAutoRemovalEvaluation}); err != nil {
			return nil, err
		}
		if inventoryService != nil {
			if database := inventoryService.Store(); database != nil {
				if err := server.recoverScheduledRemovals(database); err != nil {
					return nil, err
				}
			}
		}
	}
	return server, nil
}

// Handler is every route behind the sign-in (see authGate).
func (server *Server) Handler() http.Handler {
	gated := server.authGate(server.routes())
	return securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Other apps ask with Overtide's API key, not a session.
		if r.URL.Path == roomPath {
			server.room(w, r)
			return
		}
		gated.ServeHTTP(w, r)
	}))
}

// routes is every route, without the sign-in: tests use it directly.
func (server *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", server.page)
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte("ok")) })
	mux.HandleFunc("/ui/events", server.uiEvents)
	mux.HandleFunc("/ui/status", server.uiStatus)

	mux.HandleFunc("GET /api/session", server.session)
	mux.HandleFunc("POST /api/setup", server.setup)
	mux.HandleFunc("POST /api/login", server.login)
	mux.HandleFunc("POST /api/logout", server.logout)

	mux.HandleFunc("/api/media", server.apiMedia)
	mux.HandleFunc("/api/dashboard", server.apiDashboard)
	mux.HandleFunc("/api/torrents", server.apiTorrents)
	mux.HandleFunc("/api/unmanaged", server.apiUnmanaged)
	mux.HandleFunc("/api/files", server.apiFiles)
	mux.HandleFunc("/api/refresh", server.refresh)
	mux.HandleFunc("/api/plan", server.plan)

	// Each area's pages ask their own part of the API (routes_*.go).
	server.homeRoutes(mux)
	server.storageRoutes(mux)
	server.libraryRoutes(mux)
	server.torrentsRoutes(mux)
	server.removalRoutes(mux)
	server.servicesRoutes(mux)
	server.settingsRoutes(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, http.StatusNotFound, "no such request") })
	return sameOriginWrites(mux)
}

// notice is an error whose text is shown to people as it is, a sentence.
type notice string

func (n notice) Error() string { return string(n) }

func (server *Server) planningReliable(r inventory.Reliability) bool {
	return r.Inventory && r.Valuation && r.FileModel == "reliable" && (server.tasks == nil || !server.tasks.ConsistencyPending())
}

// deviceViews builds one cleanup plan per known storage device, since there
// is no longer a single global storage path to build one plan against.
// excluded (see cleanup.Build) is applied to every device uniformly — an
// action key from a different device never matches anything there, so a
// non-nil set computed for one specific device (the cleanup-plan overlay's
// live "exclude and backfill" recompute) is harmless to pass through here
// unchanged for every caller that only ever means "no exclusions" (nil).
func (server *Server) deviceViews(items []model.Media, torrents []model.Torrent, planningReliable bool, excluded map[string]bool) []deviceView {
	devices := server.inv.StorageDevices()
	if len(devices) == 0 {
		return nil
	}
	mediaByDevice := server.inv.MediaByDevice(items)
	torrentsByDevice := server.inv.TorrentsByDevice(torrents)
	cfg := server.inv.Config()
	views := make([]deviceView, 0, len(devices))
	for _, device := range devices {
		target, critical := cfg.ThresholdsFor(device.RepresentativePath)
		enabled := cfg.AutomaticRemovalEnabledFor(device.RepresentativePath)
		var p cleanup.Plan
		var planErr error
		if enabled {
			p, planErr = cleanup.Build(device.RepresentativePath, device.OtherBytes, target, critical, mediaByDevice[device.RepresentativePath], torrentsByDevice[device.RepresentativePath], planningReliable, excluded, cfg.Removal.CopiesWithTitles)
		} else {
			// No point building a plan nothing will ever act on — an explicit
			// per-device opt-out, same reasoning as the global AutoMode
			// disabled check already skipping evaluation entirely rather than
			// just withholding submission.
			p = cleanup.Plan{Path: device.RepresentativePath, TargetUsagePercent: target, CriticalUsagePercent: critical}
		}
		views = append(views, deviceView{Storage: device, Plan: p, PlanErr: planErr, Name: cfg.DeviceName(device.RepresentativePath), Enabled: enabled})
	}
	return views
}

// storageViews is deviceViews without the cleanup plans, for a page that
// only shows the disks (Home).
func (server *Server) storageViews() []deviceView {
	cfg := server.inv.Config()
	var views []deviceView
	for _, device := range server.inv.StorageDevices() {
		views = append(views, deviceView{Storage: device, Name: cfg.DeviceName(device.RepresentativePath), Enabled: cfg.AutomaticRemovalEnabledFor(device.RepresentativePath)})
	}
	return views
}

// deviceView pairs one physical storage device with its own cleanup plan:
// removing the single global storage path means there is no longer one
// answer to "is cleanup needed," only a per-device one.
type deviceView struct {
	Storage inventory.StorageDevice
	Plan    cleanup.Plan
	PlanErr error
	// Name is the device's user-chosen label (config.DeviceThreshold.Name),
	// empty until someone sets one via the Device settings overlay.
	Name string
	// Enabled is false when the user has explicitly opted this specific
	// device out of automatic removal (config.DeviceThreshold.
	// AutomaticRemovalDisabled) — Plan is a bare, never-built zero plan in
	// that case, not a real evaluation.
	Enabled bool
}

func queryInt(r *http.Request, key string, fallback int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	return v
}
func allowedPageSize(v int) int {
	for _, n := range []int{25, 50, 100, 250} {
		if v == n {
			return v
		}
	}
	return 50
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func normalizeTorrentStatus(v string) string {
	return model.NormalizeTorrentStatus(v)
}

func detailErrString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func Run(ctx context.Context, addr string, h http.Handler) error {
	// A client gets 10 seconds to send its request headers, and a
	// connection left idle closes after 2 minutes, as in Overfin.
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	log.Printf("[http] %s listening on %s", product.Name, addr)
	e := srv.ListenAndServe()
	if e == http.ErrServerClosed {
		return nil
	}
	return fmt.Errorf("http: %w", e)
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func humanDuration(sec int64) string {
	if sec < 0 || sec >= 8640000 {
		return "—"
	}
	d := time.Duration(sec) * time.Second
	if d >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int((d%(24*time.Hour))/time.Hour))
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int((d%time.Hour)/time.Minute))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm %ds", int(d/time.Minute), int((d%time.Minute)/time.Second))
	}
	return fmt.Sprintf("%ds", sec)
}
func shortPath(v string) string {
	const max = 88
	if len(v) <= max {
		return v
	}
	const left, right = 34, 51
	if len(v) <= left+right+3 {
		return v
	}
	return v[:left] + "..." + v[len(v)-right:]
}

// fileOwnerLabel describes which service's root a file path was discovered
// under (its StorageContexts), for display next to a file row so "who owns
// this path" doesn't require cross-referencing the Services page.
func fileOwnerLabel(contexts []model.StorageContext) string {
	names := make([]string, 0, len(contexts))
	seen := map[string]bool{}
	for _, context := range contexts {
		name := context.ServiceName
		if name == "" {
			name = context.RootLabel
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// filePeerOwnerLabel describes which media item or torrent a hardlinked
// sibling path belongs to, so a "Files" section's other known paths read as
// more than an unexplained list of strings.
func filePeerOwnerLabel(peer inventory.FilePeer) string {
	owners := make([]string, 0, len(peer.Media)+len(peer.Torrents))
	for _, ref := range peer.Media {
		if ref.Title == "" {
			continue
		}
		if ref.Year > 0 {
			owners = append(owners, fmt.Sprintf("%s (%d)", ref.Title, ref.Year))
		} else {
			owners = append(owners, ref.Title)
		}
	}
	for _, torrent := range peer.Torrents {
		if torrent.Name == "" {
			continue
		}
		owners = append(owners, "torrent: "+torrent.Name)
	}
	return strings.Join(owners, ", ")
}

// torrentCleanupActions and mediaCleanupActions split a device's cleanup
// candidates into the same two tiers cleanup.rank() already computes
// internally (every torrent-only candidate before any media/season one),
// so the Storage page can present them as two grouped, labeled lists
// instead of one flat one where the grouping is invisible.
func torrentCleanupActions(actions []cleanup.Action) []cleanup.Action {
	filtered := make([]cleanup.Action, 0, len(actions))
	for _, action := range actions {
		if action.Kind == cleanup.StandaloneTorrent {
			filtered = append(filtered, action)
		}
	}
	return filtered
}

func mediaCleanupActions(actions []cleanup.Action) []cleanup.Action {
	filtered := make([]cleanup.Action, 0, len(actions))
	for _, action := range actions {
		if action.Kind != cleanup.StandaloneTorrent {
			filtered = append(filtered, action)
		}
	}
	return filtered
}

// torrentActionContext describes why a standalone-torrent cleanup candidate
// is only a torrent action rather than part of a media/season action: its
// association state, and — the fact that most directly answers "would this
// touch my library copy" — whether it's a Current torrent proven to be an
// independent, non-hardlinked copy rather than the same physical file as
// its media. Includes the related media title when one is known, so a
// bare release name isn't the only clue to what this actually is.
func torrentActionContext(t model.Torrent) string {
	var parts []string
	switch model.NormalizeTorrentStatus(t.AssociationStatus) {
	case model.TorrentCurrent:
		if t.MediaHardlinkKnown && !t.MediaHardlinked {
			parts = append(parts, "independent copy, not hardlinked to its media")
		} else {
			parts = append(parts, "current")
		}
	case model.TorrentSuperseded:
		parts = append(parts, "superseded")
	case model.TorrentOrphaned:
		parts = append(parts, "orphaned")
	default:
		parts = append(parts, "unassociated")
	}
	mediaRefs := t.MediaItems
	if len(mediaRefs) == 0 {
		mediaRefs = t.FormerMediaItems
	}
	for _, ref := range mediaRefs {
		if ref.Title == "" {
			continue
		}
		if ref.Year > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d)", ref.Title, ref.Year))
		} else {
			parts = append(parts, ref.Title)
		}
	}
	return strings.Join(parts, " · ")
}

func unixTime(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	return time.Unix(sec, 0).Local().Format("2006-01-02 15:04:05")
}
