package httpui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/pacing"
	"github.com/overfin/overtide/internal/services/arr"
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
	// Off: said once, and no time promised for anything.
	if code != http.StatusOK || now.Doing != "Adding is off" || slices.Contains(now.Holding, "Adding is off.") {
		t.Fatalf("now: %d %+v", code, now)
	}
	for _, row := range now.Next {
		if row.When != "" {
			t.Errorf("a time while off: %+v", row)
		}
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

// Overtide tries its address with a token of its own, which gets a test
// event in and nothing else, and gives the secret only to the webhook it
// then saves.
func TestArrEventsProbeToken(t *testing.T) {
	fake := arrfake.New(*arrfake.Day("2026-11-06"))
	cfg := arrfake.Config(t, fake, config.ManageAll)
	server, handler := libraryServer(t, cfg)
	adder := adding.New(server.inv)
	server.SetAdding(adder, pacing.New(func() *prowlarr.Client { return nil }))
	secret, err := adder.EventsSecret()
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, password, _ := r.BasicAuth()
		if password == secret {
			t.Error("a try was sent the webhook's secret")
		}
		tokens = append(tokens, password)
		added := httptest.NewRequest(http.MethodPost, adding.EventsPath, strings.NewReader(`{"eventType":"MovieAdded"}`))
		added.Header.Set("Content-Type", "application/json")
		added.SetBasicAuth(adding.EventsUser, password)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, added)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("a token's add event: %d", response.Code)
		}
		handler.ServeHTTP(w, r)
	}))
	defer receiver.Close()
	adder.SeenAt(receiver.URL)
	adder.Connect(context.Background())
	if got := server.inv.Config().Adding.EventsURL; got != receiver.URL || adder.EventsProblem() != "" || len(tokens) != 1 {
		t.Fatalf("address %q, problem %q, tries %d", got, adder.EventsProblem(), len(tokens))
	}
	hook := fake.Notifications["radarr"][1]
	if arr.Field(hook, "password") != secret {
		t.Errorf("the webhook saved: %v", hook)
	}
	request := httptest.NewRequest(http.MethodPost, adding.EventsPath, strings.NewReader(`{"eventType":"Test"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(adding.EventsUser, tokens[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Errorf("the token after its try: %d", response.Code)
	}
}
