package httpui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
)

func TestStorageShowsPerServiceDetailAndRootPaths(t *testing.T) {
	out := storageJSONFrom(storageData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{
					RootLabels: []string{"downloads", "movies"}, RepresentativePath: "/data/movies", Filesystem: "ext2/ext3/ext4",
					Available: true, TotalBytes: 1000, FreeBytes: 400, UsedBytes: 600,
					Claimed:        []inventory.ClaimedSegment{{Service: "Movies", Bytes: 500}},
					UnmanagedBytes: 50, OtherBytes: 50,
					ServiceRoots: map[string][]string{"Movies": {"/data/movies", "/data/movies-4k"}},
				},
				Plan: cleanup.Plan{Available: true, UsagePercent: 60, TargetUsagePercent: 90, Message: "No cleanup: 60.00% used (target 90.0%)"},
				Name: "Media Drive", Enabled: true,
			},
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/broken", Available: false, Error: "permission denied"},
			},
		},
	})
	disk := out.Devices[0]
	if disk.Name != "Media Drive" || disk.Filesystem != "ext2/ext3/ext4" || strings.Join(disk.RootLabels, ", ") != "downloads, movies" {
		t.Fatalf("identity: %+v", disk)
	}
	if len(disk.Claimed) != 1 || disk.Claimed[0].Service != "Movies" || disk.Claimed[0].Bytes != 500 ||
		strings.Join(disk.Claimed[0].Roots, " ") != "/data/movies /data/movies-4k" {
		t.Fatalf("claims: %+v", disk.Claimed)
	}
	if disk.UnmanagedBytes != 50 || disk.Plan == nil || !strings.HasPrefix(disk.Plan.Message, "No cleanup") || disk.Plan.TargetUsagePercent != 90 {
		t.Fatalf("usage and plan: %+v %+v", disk, disk.Plan)
	}
	if broken := out.Devices[1]; broken.Available || broken.Error != "permission denied" {
		t.Fatalf("unavailable device: %+v", broken)
	}
}

// A torrent client's incomplete-downloads directory can live on another
// physical disk than its normal save path, so the same service claims
// bytes on two devices. Each device lists only its own roots of the
// service, never every root the service has.
func TestStorageScopesServiceRootsToTheirOwnDevice(t *testing.T) {
	out := storageJSONFrom(storageData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{
					RepresentativePath: "/data/media", Available: true, TotalBytes: 1000, FreeBytes: 900, UsedBytes: 100,
					Claimed:      []inventory.ClaimedSegment{{Service: "Downloader", Bytes: 100}},
					ServiceRoots: map[string][]string{"Downloader": {"/data/downloads/complete"}},
				},
				Name: "Media", Enabled: true,
			},
			{
				Storage: inventory.StorageDevice{
					RepresentativePath: "/torrent-cache", Available: true, TotalBytes: 500, FreeBytes: 400, UsedBytes: 100,
					Claimed:      []inventory.ClaimedSegment{{Service: "Downloader", Bytes: 100}},
					ServiceRoots: map[string][]string{"Downloader": {"/torrent-cache"}},
				},
				Name: "Torrent cache", Enabled: true,
			},
		},
	})
	if roots := out.Devices[0].Claimed[0].Roots; len(roots) != 1 || roots[0] != "/data/downloads/complete" {
		t.Fatalf("the Media device's roots: %v", roots)
	}
	if roots := out.Devices[1].Claimed[0].Roots; len(roots) != 1 || roots[0] != "/torrent-cache" {
		t.Fatalf("the cache device's roots: %v", roots)
	}
}

// A device's card shows the plan's aggregate (how many actions, how many
// bytes), never its rows: those are the plan window's. With nothing to
// clean, the card says there's no action (no plan button).
func TestStorageCardCarriesThePlansAggregateOnly(t *testing.T) {
	withActions := storageJSONFrom(storageData{Devices: []deviceView{{
		Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 100, UsedBytes: 900},
		Enabled: true,
		Plan: cleanup.Plan{
			Available: true, UsagePercent: 90, TargetUsagePercent: 80, NeedBytes: 100, SelectedBytes: 30, Message: "Need to reclaim 30.0 B to reach 80.0% usage",
			Actions: []cleanup.Action{{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Name: "Old Release"}}, ReclaimableBytes: 10}},
		},
	}}})
	plan := withActions.Devices[0].Plan
	if plan.Actions != 1 || plan.NeedBytes != 100 || plan.SelectedBytes != 30 {
		t.Fatalf("aggregate: %+v", plan)
	}
	if raw, _ := json.Marshal(withActions); strings.Contains(string(raw), "Old Release") {
		t.Fatalf("the card carries the plan's rows: %s", raw)
	}
	noActions := storageJSONFrom(storageData{Devices: []deviceView{{
		Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 900, UsedBytes: 100},
		Enabled: true,
		Plan:    cleanup.Plan{Available: true, UsagePercent: 10, TargetUsagePercent: 80, Message: "Under target"},
	}}})
	if plan := noActions.Devices[0].Plan; plan.Actions != 0 || plan.NeedBytes != 0 {
		t.Fatalf("nothing to clean: %+v", plan)
	}
}

// The plan window lists every kind of action, each named as it is. A
// StandaloneSeason action has no torrents at all: an earlier version
// indexed into its empty Torrents and crashed.
func TestCleanupPlanRowsForEveryActionKind(t *testing.T) {
	out := cleanupPlanJSONFrom(cleanupPlanData{
		RepresentativePath: "/data/movies",
		Plan: cleanup.Plan{
			Available: true, UsagePercent: 90, TargetUsagePercent: 80, NeedBytes: 100, SelectedBytes: 30, Message: "Need to reclaim 30.0 B to reach 80.0% usage",
			Actions: []cleanup.Action{
				{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Name: "Old Release"}}, ReclaimableBytes: 10},
				{Kind: cleanup.StandaloneMedia, Media: model.Media{Title: "Lonely Movie"}, ReclaimableBytes: 10},
				{Kind: cleanup.HardlinkedBundle, Media: model.Media{Title: "Bundled Movie"}, Torrents: []model.Torrent{{Name: "Bundled Release"}}, ReclaimableBytes: 10},
				{Kind: cleanup.StandaloneSeason, Media: model.Media{Title: "Some Show"}, Season: &model.Season{Number: 3}, ReclaimableBytes: 10},
			},
		},
	})
	var titles []string
	for _, row := range append(append([]cleanupRowJSON{}, out.Torrents...), out.Media...) {
		titles = append(titles, row.Title)
	}
	if strings.Join(titles, "|") != "Old Release|Lonely Movie|Bundled Movie + 1 hardlinked torrent(s)|Some Show · Season 3" {
		t.Fatalf("rows: %q", titles)
	}
	if out.SelectedBytes != 30 || out.Message != "Need to reclaim 30.0 B to reach 80.0% usage" {
		t.Fatalf("aggregate: %+v", out)
	}
	if bundle := out.Media[1]; bundle.With != "Torrents removed with it: Bundled Release" {
		t.Fatalf("bundle: %+v", bundle)
	}
}

// A lone torrent's row says what it is to the library: its association,
// whether it's a proven-independent (non-hardlinked) copy, and which title
// it relates to. Torrents and titles are two groups, torrents first, as
// cleanup.rank() orders them.
func TestCleanupPlanGroupsActionsAndExplainsTorrents(t *testing.T) {
	out := cleanupPlanJSONFrom(cleanupPlanData{
		RepresentativePath: "/data/movies",
		Plan: cleanup.Plan{
			Available: true, NeedBytes: 100, SelectedBytes: 20,
			Actions: []cleanup.Action{
				{Kind: cleanup.StandaloneMedia, Media: model.Media{Title: "Lonely Movie", ServiceName: "Movies 4K"}, ReclaimableBytes: 10},
				{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Name: "Show.S03E01.mkv", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: false, MediaItems: []model.MediaRef{{Title: "Some Show", Year: 2024}}}}, ReclaimableBytes: 10},
			},
		},
	})
	if len(out.Torrents) != 1 || len(out.Media) != 1 {
		t.Fatalf("groups: %+v / %+v", out.Torrents, out.Media)
	}
	torrent := out.Torrents[0]
	if torrent.Title != "Show.S03E01.mkv" || !strings.Contains(torrent.Meta, "independent copy, not hardlinked to its media") || !strings.Contains(torrent.Meta, "Some Show (2024)") || !strings.HasSuffix(torrent.Meta, " · 10.0 B") {
		t.Fatalf("torrent row: %+v", torrent)
	}
	if media := out.Media[0]; media.Title != "Lonely Movie" || media.Meta != "Movies 4K · 10.0 B" {
		t.Fatalf("media row: %+v", media)
	}
}

// Each row carries the key Skip and Keep find it again with, and what
// Clean sends back; the skips made so far come back with the plan, so the
// window's next request rebuilds the identical filtered plan.
func TestCleanupPlanCarriesKeysAndSkips(t *testing.T) {
	action := cleanup.Action{Kind: cleanup.StandaloneTorrent, Torrents: []model.Torrent{{Hash: "ABC123", ServiceID: "qb-1", Name: "Old Release"}}, ReclaimableBytes: 10}
	out := cleanupPlanJSONFrom(cleanupPlanData{
		RepresentativePath: "/data/movies",
		Excluded:           []string{"media:movie:radarr-1:1:"},
		Plan:               cleanup.Plan{Available: true, NeedBytes: 100, SelectedBytes: 10, Actions: []cleanup.Action{action}},
	})
	if out.Path != "/data/movies" || len(out.Excluded) != 1 || out.Excluded[0] != "media:movie:radarr-1:1:" {
		t.Fatalf("plan: %+v", out)
	}
	if row := out.Torrents[0]; row.Key != cleanup.ActionKey(action) || row.Shown != shownKey(action) {
		t.Fatalf("row keys: %+v", row)
	}
}

func TestCleanupPlanWithNothingToClean(t *testing.T) {
	out := cleanupPlanJSONFrom(cleanupPlanData{RepresentativePath: "/data/movies"})
	if out.Torrents == nil || out.Media == nil || len(out.Torrents)+len(out.Media) != 0 || out.Excluded == nil {
		t.Fatalf("%+v", out)
	}
}

// With removal.auto_mode off, the notion of "what we'd suggest removing"
// doesn't exist: the page says so (autoRemovalDisabled). The bar and usage
// numbers are a different concern and are still there.
func TestStorageSaysWhenAutomaticRemovalIsOff(t *testing.T) {
	out := storageJSONFrom(storageData{
		AutoRemovalDisabled: true,
		Devices: []deviceView{{
			Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 100, UsedBytes: 900},
			Enabled: true,
		}},
	})
	if !out.AutoRemovalDisabled || out.Devices[0].OvertideUsedBytes != 900 || out.Devices[0].CapacityBytes != 1000 {
		t.Fatalf("%+v", out)
	}
}

// One device opted out drops its own threshold marker and plan section
// (enabled false), without touching its bar or any other device.
func TestStorageSaysWhenADeviceIsOptedOut(t *testing.T) {
	out := storageJSONFrom(storageData{Devices: []deviceView{
		{Storage: inventory.StorageDevice{RepresentativePath: "/data/movies", Available: true, TotalBytes: 1000, FreeBytes: 100, UsedBytes: 900}, Enabled: false,
			Plan: cleanup.Plan{TargetUsagePercent: 80, CriticalUsagePercent: 95}},
		{Storage: inventory.StorageDevice{RepresentativePath: "/data/series", Available: true, TotalBytes: 1000, FreeBytes: 500, UsedBytes: 500}, Enabled: true},
	}})
	if out.Devices[0].Enabled || !out.Devices[1].Enabled || out.Devices[0].OvertideUsedBytes != 900 {
		t.Fatalf("%+v", out.Devices)
	}
}

func TestStorageShowsCapabilities(t *testing.T) {
	out := storageJSONFrom(storageData{Capabilities: []inventory.RootCapability{
		{Path: "/data/movies", ServiceName: "Movies", ServiceType: "radarr", Reachable: true, Filesystem: "ext4", FreeBytes: 400, ReservedBytes: 20},
		{Path: "/data/missing", ServiceName: "Downloader", ServiceType: "qbittorrent", Purpose: "incomplete downloads", Reachable: false, SelfReportedCount: 3, SelfReportedSizeBytes: 12345},
	}})
	if c := out.Capabilities[0]; !c.Reachable || c.Filesystem != "ext4" || c.FreeBytes != 400 || c.ReservedBytes != 20 {
		t.Fatalf("reachable root: %+v", c)
	}
	if c := out.Capabilities[1]; c.Reachable || c.Purpose != "incomplete downloads" || c.SelfReportedCount != 3 || c.SelfReportedSizeBytes != 12345 {
		t.Fatalf("unreachable root: %+v", c)
	}
}

func TestStorageWithNoKnownDevices(t *testing.T) {
	out := storageJSONFrom(storageData{})
	if out.Devices == nil || out.Capabilities == nil || out.Kept == nil || len(out.Devices) != 0 {
		t.Fatalf("%+v", out)
	}
}

// storageTestServer is a server whose settings are saved to a real file,
// read back by the tests.
func storageTestServer(t *testing.T, configJSON string) (*Server, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server, configPath
}

// The byte totals live updates ask for every few seconds come from the
// devices and the thresholds alone, never a removal plan (cleanup.Build is
// real work that has nothing to do with bytes ticking), and only to GET.
func TestStorageStatsServesCheapJSONWithoutARemovalPlan(t *testing.T) {
	server, _ := storageTestServer(t, `{"storage":{}}`)
	handler := server.routes()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage/stats", nil))
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "[]" {
		t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	posted := httptest.NewRecorder()
	handler.ServeHTTP(posted, httptest.NewRequest(http.MethodPost, "/api/storage/stats", nil))
	if posted.Code < 400 {
		t.Fatalf("POST answered %d", posted.Code)
	}
}

// The device settings window starts from the device's current thresholds
// and name, and whether automatic removal looks at it.
func TestDeviceSettingsShowCurrentThresholds(t *testing.T) {
	server, _ := storageTestServer(t, `{"storage":{"device_thresholds":{
		"/data/movies":{"target_usage_percent":70,"critical_usage_percent":85,"name":"Media Drive"},
		"/data/off":{"target_usage_percent":80,"critical_usage_percent":90,"automatic_removal_disabled":true}}}}`)
	handler := server.routes()
	for _, tc := range []struct {
		path               string
		target, critical   float64
		name               string
		automaticRemovalOn bool
	}{
		{"/data/movies", 70, 85, "Media Drive", true},
		{"/data/off", 80, 90, "", false},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage/device?path="+url.QueryEscape(tc.path), nil))
		got := decode[deviceSettingsJSON](t, recorder)
		if recorder.Code != http.StatusOK || got.Path != tc.path || got.TargetUsagePercent != tc.target || got.CriticalUsagePercent != tc.critical ||
			got.AutomaticRemovalEnabled != tc.automaticRemovalOn || (tc.name != "" && got.Name != tc.name) || got.RootLabels == nil {
			t.Fatalf("%s: %d %+v", tc.path, recorder.Code, got)
		}
	}
}

// A device's settings are saved from its window: name and thresholds
// persisted; a critical threshold not above the target is refused.
func TestSetDeviceSettingsPersists(t *testing.T) {
	server, configPath := storageTestServer(t, `{"storage":{}}`)
	handler := server.routes()
	devicePath := "/data/movies"
	response := send(t, handler, http.MethodPut, "/api/storage/device", deviceSettingsJSON{Path: devicePath, Name: "Media Drive", TargetUsagePercent: 80, CriticalUsagePercent: 92, AutomaticRemovalEnabled: true}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if target, critical := reloaded.ThresholdsFor(devicePath); target != 80 || critical != 92 {
		t.Fatalf("thresholds: %v %v", target, critical)
	}
	if got := reloaded.DeviceName(devicePath); got != "Media Drive" {
		t.Fatalf("name: %q", got)
	}
	if !reloaded.AutomaticRemovalEnabledFor(devicePath) {
		t.Fatal("automatic removal turned off")
	}
	refused := send(t, handler, http.MethodPut, "/api/storage/device", deviceSettingsJSON{Path: devicePath, TargetUsagePercent: 90, CriticalUsagePercent: 90, AutomaticRemovalEnabled: true}, nil)
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), `"error"`) {
		t.Fatalf("critical equal to the target: %d %q", refused.Code, refused.Body.String())
	}
	// Only JSON is taken.
	form := postForm(t, handler, "/api/storage/device", url.Values{"path": {devicePath}}, nil)
	if form.Code < 400 {
		t.Fatalf("a form was taken: %d", form.Code)
	}
}

// A device no longer known (removed, or a stale window) has an empty plan,
// not an error; Clean and Keep on it are refused with 404.
func TestCleanupPlanForAnUnknownDevice(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage/plan?path=/data/movies", nil))
	if plan := decode[cleanupPlanJSON](t, recorder); recorder.Code != http.StatusOK || len(plan.Torrents)+len(plan.Media) != 0 {
		t.Fatalf("plan: %d %+v", recorder.Code, plan)
	}
	for _, path := range []string{"/api/storage/plan/clean", "/api/storage/plan/protect"} {
		response := post(t, handler, path, cleanupPlanRequest{Path: "/data/movies", Key: "torrent:abc"}, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s: %d %q", path, response.Code, response.Body.String())
		}
	}
}

// A torrent is kept and taken off the Kept list again; a key is required.
func TestKeepAndUnkeep(t *testing.T) {
	server, configPath := storageTestServer(t, `{"storage":{}}`)
	handler := server.routes()
	key := config.KeptTorrentKey("ABC123")
	if response := post(t, handler, "/api/storage/keep", keepRequest{Key: key, Name: "Old Release"}, nil); response.Code != http.StatusOK {
		t.Fatalf("keep: %d %q", response.Code, response.Body.String())
	}
	kept := decode[storageJSON](t, getPath(t, handler, "/api/storage", nil)).Kept
	if len(kept) != 1 || kept[0].Key != key || kept[0].Name != "Old Release" {
		t.Fatalf("kept: %+v", kept)
	}
	if response := post(t, handler, "/api/storage/keep", keepRequest{Key: key, Unkeep: true}, nil); response.Code != http.StatusOK {
		t.Fatalf("unkeep: %d", response.Code)
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Kept) != 0 {
		t.Fatalf("still kept: %+v", reloaded.Kept)
	}
	if response := post(t, handler, "/api/storage/keep", keepRequest{Key: " "}, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("no key: %d", response.Code)
	}
}
