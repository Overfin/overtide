package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/overfin/overtide/internal/config"
)

// fakeProwlarrApps is a Prowlarr with a Radarr and a Sonarr app; refuse
// makes it refuse saving the app with that ID (its test fails).
type fakeProwlarrApps struct {
	mu     sync.Mutex
	apps   map[int]map[string]any
	refuse int
	synced int
}

func newFakeProwlarrApps() *fakeProwlarrApps {
	app := func(id int, kind string) map[string]any {
		return map[string]any{"id": id, "name": kind, "implementation": kind, "fields": []any{
			map[string]any{"name": "prowlarrUrl", "value": "http://prowlarr:9696"},
			map[string]any{"name": "apiKey", "value": "********"},
		}}
	}
	return &fakeProwlarrApps{apps: map[int]map[string]any{1: app(1, "Radarr"), 2: app(2, "Sonarr")}}
}

func (f *fakeProwlarrApps) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/api/v1/applications" && r.Method == http.MethodGet:
		var out []map[string]any
		for _, id := range []int{1, 2} {
			out = append(out, f.apps[id])
		}
		json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(r.URL.Path, "/api/v1/applications/") && r.Method == http.MethodPut:
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/v1/applications/"))
		if id == f.refuse {
			http.Error(w, `[{"errorMessage":"Radarr cannot connect to Prowlarr"}]`, http.StatusBadRequest)
			return
		}
		var app map[string]any
		json.NewDecoder(r.Body).Decode(&app)
		f.apps[id] = app
		w.WriteHeader(http.StatusAccepted)
	case r.URL.Path == "/api/v1/command":
		f.synced++
		w.Write([]byte(`{}`))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeProwlarrApps) address(id int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, raw := range f.apps[id]["fields"].([]any) {
		if field := raw.(map[string]any); field["name"] == "prowlarrUrl" {
			return field["value"].(string)
		}
	}
	return ""
}

func TestSetProwlarrRoutesAndPutsBack(t *testing.T) {
	fake := newFakeProwlarrApps()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	service, configPath := keepTestService(t, `{"storage":{}}`)

	if err := service.SetProwlarr(context.Background(), srv.URL, "key", "http://overtide:8089", true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2} {
		if got := fake.address(id); got != "http://overtide:8089/prowlarr" {
			t.Fatalf("app %d: %s", id, got)
		}
	}
	saved, _ := config.Load(configPath)
	if !saved.Prowlarr.Route || saved.Prowlarr.Replaced["1"] != "http://prowlarr:9696" || saved.Prowlarr.APIKey != "key" || fake.synced != 1 {
		t.Fatalf("saved: %+v, synced %d", saved.Prowlarr, fake.synced)
	}

	// Off, with the key left empty (kept): their own address comes back.
	if err := service.SetProwlarr(context.Background(), srv.URL, "", "http://overtide:8089", false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{1, 2} {
		if got := fake.address(id); got != "http://prowlarr:9696" {
			t.Fatalf("app %d after: %s", id, got)
		}
	}
	saved, _ = config.Load(configPath)
	if saved.Prowlarr.Route || len(saved.Prowlarr.Replaced) != 0 || saved.Prowlarr.APIKey != "key" || fake.synced != 2 {
		t.Fatalf("saved after: %+v, synced %d", saved.Prowlarr, fake.synced)
	}
}

// Prowlarr refusing the second app (its test fails) puts the first back,
// and nothing is routed.
func TestSetProwlarrRefusedPutsBack(t *testing.T) {
	fake := newFakeProwlarrApps()
	fake.refuse = 2
	srv := httptest.NewServer(fake)
	defer srv.Close()
	service, configPath := keepTestService(t, `{"storage":{}}`)
	err := service.SetProwlarr(context.Background(), srv.URL, "key", "http://overtide:8089", true)
	if err == nil || !strings.Contains(err.Error(), "cannot connect") {
		t.Fatalf("error: %v", err)
	}
	if got := fake.address(1); got != "http://prowlarr:9696" {
		t.Fatalf("app 1 not put back: %s", got)
	}
	saved, _ := config.Load(configPath)
	if saved.Prowlarr.Route || fake.synced != 0 {
		t.Fatalf("saved: %+v, synced %d", saved.Prowlarr, fake.synced)
	}
}
