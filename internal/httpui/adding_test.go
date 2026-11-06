package httpui

import (
	"net/http"
	"testing"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/pacing"
	"github.com/overfin/overtide/internal/services/prowlarr"
)

// Without adding, its routes say so; with it, the Adding page, the live
// panel and the settings answer, adding off.
func TestAddingRoutes(t *testing.T) {
	server, handler := libraryServer(t, config.Config{})
	if _, code := libraryGet[map[string]any](t, handler, "/api/adding"); code != http.StatusServiceUnavailable {
		t.Fatalf("without adding: %d", code)
	}
	server.SetAdding(adding.New(server.inv), pacing.New(func() *prowlarr.Client { return nil }))
	list, code := libraryGet[addingData](t, handler, "/api/adding")
	if code != http.StatusOK || list.Mode != config.AddingOff || list.Manage != config.ManageOwn {
		t.Fatalf("adding: %d %+v", code, list)
	}
	now, code := libraryGet[nowData](t, handler, "/api/adding/now")
	if code != http.StatusOK || len(now.Holding) == 0 || now.Holding[0] != "Adding is off." {
		t.Fatalf("now: %d %+v", code, now)
	}
	settings, code := libraryGet[addingSettingsData](t, handler, "/api/settings/adding")
	if code != http.StatusOK || settings.Searches != config.DefaultSearches || settings.Destinations == nil {
		t.Fatalf("settings: %d %+v", code, settings)
	}
}
