package httpui

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// Radarr's and Sonarr's events get in with the webhook's secret only, and
// a test event is heard.
func TestArrEvents(t *testing.T) {
	server, handler := libraryServer(t, config.Config{})
	adder := adding.New(server.inv)
	server.SetAdding(adder, pacing.New(func() *prowlarr.Client { return nil }))
	secret, err := adder.EventsSecret()
	if err != nil {
		t.Fatal(err)
	}
	send := func(user, password, body string) int {
		request := httptest.NewRequest(http.MethodPost, adding.EventsPath, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if user != "" {
			request.SetBasicAuth(user, password)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	for _, c := range []struct{ user, password string }{{"", ""}, {adding.EventsUser, "wrong"}, {"someone", secret}} {
		if code := send(c.user, c.password, `{"eventType":"Test"}`); code != http.StatusUnauthorized {
			t.Errorf("%q/%q: %d", c.user, c.password, code)
		}
	}
	if code := send(adding.EventsUser, secret, `not json`); code != http.StatusBadRequest {
		t.Errorf("not an event: %d", code)
	}
	for _, event := range []string{"Test", "MovieAdded", "Grab"} {
		if code := send(adding.EventsUser, secret, `{"eventType":"`+event+`"}`); code != http.StatusNoContent {
			t.Errorf("%s: %d", event, code)
		}
	}
}
