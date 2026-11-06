package adding

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/arr"
)

// With "Manage all", Overtide finds the address Radarr and Sonarr reach it
// at (the one a test event comes through, with the secret), keeps it, and
// gives both a webhook for the titles they add, once. In dry run nothing
// changes; when no address works, it says so and saves nothing.
func TestConnect(t *testing.T) {
	now := *arrfake.Day("2026-11-06")
	setup := func(answer bool) (*arrfake.Arrs, *Service, config.Config) {
		fake := arrfake.New(now)
		cfg := arrfake.Config(t, fake, config.ManageAll)
		var service *Service
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			secret, _ := service.EventsSecret()
			if user, password, ok := r.BasicAuth(); !answer || !ok || user != EventsUser || password != secret || r.URL.Path != EventsPath {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			service.HeardTest()
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(receiver.Close)
		_, port, _ := net.SplitHostPort(strings.TrimPrefix(receiver.URL, "http://"))
		cfg.Server.Listen = ":" + port
		cfg.Services[0].Name, cfg.Services[1].Name = "Radarr", "Sonarr"
		service = newTestService(t, cfg, now)
		return fake, service, cfg
	}

	fake, service, cfg := setup(true)
	if err := service.connect(context.Background(), cfg, true); err != nil {
		t.Fatal(err)
	}
	if changes := fake.Mutations(); len(changes) != 0 || service.inv.Config().Adding.EventsURL != "" {
		t.Errorf("dry run changed: %v", changes)
	}
	if err := service.connect(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	address := service.inv.Config().Adding.EventsURL
	_, port, _ := net.SplitHostPort(cfg.Server.Listen)
	if !strings.HasSuffix(address, ":"+port) || service.EventsProblem() != "" {
		t.Fatalf("address %q, problem %q", address, service.EventsProblem())
	}
	secret, _ := service.EventsSecret()
	for app, event := range map[string]string{"radarr": "onMovieAdded", "sonarr": "onSeriesAdd"} {
		hooks := fake.Notifications[app]
		if len(hooks) != 1 {
			t.Fatalf("%s: %v", app, hooks)
		}
		hook := hooks[1]
		if hook["name"] != webhookName || hook[event] != true || hook["onGrab"] != false || arr.Field(hook, "url") != address+EventsPath ||
			arr.Field(hook, "username") != EventsUser || arr.Field(hook, "password") != secret {
			t.Errorf("%s's webhook: %v", app, hook)
		}
	}
	cfg = service.inv.Config()
	before := len(fake.Mutations())
	if err := service.connect(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	if changes := fake.Mutations()[before:]; len(changes) != 0 {
		t.Errorf("saved again: %v", changes)
	}

	fake, service, cfg = setup(false)
	if err := service.connect(context.Background(), cfg, false); err != nil {
		t.Fatal(err)
	}
	if service.EventsProblem() == "" || service.inv.Config().Adding.EventsURL != "" || len(fake.Notifications["radarr"]) != 0 {
		t.Errorf("no address works: problem %q, address %q, %v", service.EventsProblem(), service.inv.Config().Adding.EventsURL, fake.Notifications)
	}
}
