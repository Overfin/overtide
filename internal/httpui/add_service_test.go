package httpui

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/tasks"
)

// newServiceTestServer is a server on a real config file (live config
// editing needs one), without the sign-in: its routes directly.
func newServiceTestServer(t *testing.T, manager *tasks.Manager) (http.Handler, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, nil)
	inv.SetConfigPath(configPath)
	server, err := New(inv, manager)
	if err != nil {
		t.Fatal(err)
	}
	return server.routes(), configPath
}

func fakeRadarr(t *testing.T) *httptest.Server {
	t.Helper()
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"5.0.0"}`))
	}))
	t.Cleanup(radarr.Close)
	return radarr
}

func reloadConfig(t *testing.T, configPath string) config.Config {
	t.Helper()
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return reloaded
}

func TestAddServiceEndToEndOverHTTP(t *testing.T) {
	radarr := fakeRadarr(t)
	handler, configPath := newServiceTestServer(t, nil)

	// The window's heading and choices load via GET.
	types := decode[serviceCategory](t, getPath(t, handler, "/api/services/types", nil))
	if types.Title != "Add service" || len(types.Types) != len(allServiceTypes) {
		t.Fatalf("expected the add-service choices, got %#v", types)
	}

	// Submitting the form adds, validates, and persists the service.
	response := post(t, handler, "/api/services", serviceForm{Type: "radarr", Name: "Movies", URL: radarr.URL, APIKey: "key"}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 on success, got status=%d body=%q", response.Code, response.Body.String())
	}
	reloaded := reloadConfig(t, configPath)
	if len(reloaded.Services) != 1 || reloaded.Services[0].Name != "Movies" {
		t.Fatalf("expected the service to be persisted, got %#v", reloaded.Services)
	}

	// The Services list now has it, without needing a restart.
	list := decode[servicesData](t, getPath(t, handler, "/api/services", nil))
	if len(list.Services) != 1 || list.Services[0].Name != "Movies" || list.Services[0].ID != reloaded.Services[0].ID {
		t.Fatalf("expected the Services list to show the newly added service live, got %#v", list)
	}
}

// A form another site's page posts (multipart, as fetch+FormData sends
// one) is refused before anything is done: only JSON is taken.
func TestAddServiceRefusesAForm(t *testing.T) {
	radarr := fakeRadarr(t)
	handler, configPath := newServiceTestServer(t, nil)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"type": "radarr", "name": "Movies", "url": radarr.URL, "api_key": "key"} {
		_ = writer.WriteField(key, value)
	}
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/api/services", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a multipart form: status=%d", response.Code)
	}
	if services := reloadConfig(t, configPath).Services; len(services) != 0 {
		t.Fatalf("a form added a service: %#v", services)
	}
}

// TestAddServiceSchedulesStorageDiscovery guards the whole reason live
// config editing exists: a newly added service's storage paths must be
// discovered promptly, not left to wait for the next periodic file
// reconciliation (which can be hours away). Adding a service must
// schedule the same inventory-then-files workflow a removal does.
func TestAddServiceSchedulesStorageDiscovery(t *testing.T) {
	radarr := fakeRadarr(t)
	manager := tasks.New(
		tasks.Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error { return nil }},
		tasks.Definition{ID: "files", Name: "Files", Runner: func(context.Context) error { return nil }},
	)
	if err := manager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Steps: []string{"inventory", "files"}}); err != nil {
		t.Fatal(err)
	}
	handler, _ := newServiceTestServer(t, manager)

	response := post(t, handler, "/api/services", serviceForm{Type: "radarr", Name: "Movies", URL: radarr.URL, APIKey: "key"}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 on success, got status=%d body=%q", response.Code, response.Body.String())
	}
	workflows := manager.WorkflowSnapshot()
	if len(workflows) != 1 {
		t.Fatalf("expected adding a service to schedule exactly one inventory-and-files-consistency run, got %#v", workflows)
	}
	if workflows[0].DefinitionID != "inventory-and-files-consistency" {
		t.Fatalf("unexpected workflow definition scheduled: %#v", workflows[0])
	}
	status := decode[consistencyStatusResponse](t, getPath(t, handler, "/api/services/consistency", nil))
	if status.TotalSteps != 2 {
		t.Fatalf("the progress window's status: %#v", status)
	}
}

func TestAddServiceEndToEndRejectsBadConnection(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer broken.Close()
	handler, configPath := newServiceTestServer(t, nil)

	response := post(t, handler, "/api/services", serviceForm{Type: "radarr", Name: "Movies", URL: broken.URL, APIKey: "bad"}, nil)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"error"`) {
		t.Fatalf("expected 400 on a failed connection check, got status=%d body=%q", response.Code, response.Body.String())
	}
	if services := reloadConfig(t, configPath).Services; len(services) != 0 {
		t.Fatalf("expected nothing persisted after a failed connection check, got %#v", services)
	}
}

// TestTestServiceConnectionPersistsNothing guards step 1 of the service
// setup window: a connection check must be a pure dry run, and an
// abandoned window must never leave a half-configured service behind.
func TestTestServiceConnectionPersistsNothing(t *testing.T) {
	radarr := fakeRadarr(t)
	handler, configPath := newServiceTestServer(t, nil)
	// Loading writes the file once (it gets its API key): what counts is
	// what the test writes after.
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	response := post(t, handler, "/api/services/test", serviceForm{Type: "radarr", Name: "Movies", URL: radarr.URL, APIKey: "key"}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200 for a successful test, got status=%d body=%q", response.Code, response.Body.String())
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("expected the connection test to write nothing, config changed:\nbefore=%s\nafter=%s", before, after)
	}
	if services := reloadConfig(t, configPath).Services; len(services) != 0 {
		t.Fatalf("expected no service to be persisted by a connection test, got %#v", services)
	}
}

// TestAddServiceSetsAllowAutomaticRemoval guards the add-service form's
// checkbox actually reaching config.Service.AllowAutomaticRemoval, the flag
// runAutoRemovalEvaluation checks per service before touching anything it
// owns.
func TestAddServiceSetsAllowAutomaticRemoval(t *testing.T) {
	radarr := fakeRadarr(t)
	handler, configPath := newServiceTestServer(t, nil)

	response := post(t, handler, "/api/services", serviceForm{Type: "radarr", Name: "Movies", URL: radarr.URL, APIKey: "key", AllowAutomaticRemoval: true}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got status=%d body=%q", response.Code, response.Body.String())
	}
	if services := reloadConfig(t, configPath).Services; len(services) != 1 || !services[0].AllowAutomaticRemoval {
		t.Fatalf("expected the checked checkbox to persist as AllowAutomaticRemoval=true, got %#v", services)
	}
}

// TestAddServiceFormNarrowsTypeOptionsByCategory guards the Torrents/Library
// empty-state buttons: each opens the same window with a category, which
// must narrow the Type select and change the heading, rather than always
// showing every adapter type regardless of where it was opened from.
func TestAddServiceFormNarrowsTypeOptionsByCategory(t *testing.T) {
	server, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.routes()

	cases := []struct {
		category    string
		wantTitle   string
		wantTypes   []string
		unwantTypes []string
	}{
		{"torrentclient", "Add torrent client", []string{"qbittorrent"}, []string{"radarr", "sonarr", "overfin"}},
		{"medialibrary", "Add media library", []string{"radarr", "sonarr"}, []string{"qbittorrent", "overfin"}},
		{"", "Add service", []string{"radarr", "sonarr", "qbittorrent", "overfin"}, nil},
		{"unknown", "Add service", []string{"radarr", "sonarr", "qbittorrent", "overfin"}, nil},
	}
	for _, c := range cases {
		url := "/api/services/types"
		if c.category != "" {
			url += "?category=" + c.category
		}
		got := decode[serviceCategory](t, getPath(t, handler, url, nil))
		if got.Title != c.wantTitle {
			t.Fatalf("category=%q: expected title %q, got %#v", c.category, c.wantTitle, got)
		}
		has := map[string]bool{}
		for _, option := range got.Types {
			has[option.Value] = true
		}
		for _, want := range c.wantTypes {
			if !has[want] {
				t.Fatalf("category=%q: expected type option %q, got %#v", c.category, want, got.Types)
			}
		}
		for _, unwant := range c.unwantTypes {
			if has[unwant] {
				t.Fatalf("category=%q: expected type option %q to be absent, got %#v", c.category, unwant, got.Types)
			}
		}
	}
}
