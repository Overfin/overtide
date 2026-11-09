package inventory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// A keep tag put on in Radarr or Sonarr counts at once, whatever the last
// refresh saw; an app that can't say answers an error, and its title stays.
func TestKeptNow(t *testing.T) {
	f := arrfake.New(time.Now())
	keep := f.TagID("radarr", model.TagKeep)
	other := f.TagID("radarr", "other")
	f.Films[1] = map[string]any{"id": 1, "tags": []int{other}}
	f.Films[2] = map[string]any{"id": 2, "tags": []int{other, keep}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	service := New(config.Config{Services: []config.Service{{ID: "r1", Type: "radarr", Name: "Movies", URL: srv.URL + "/radarr", APIKey: "key"}}}, nil)
	ctx := context.Background()
	if kept, err := service.KeptNow(ctx, model.Movie, "r1", 1); kept || err != nil {
		t.Errorf("not tagged: %v, %v", kept, err)
	}
	if kept, err := service.KeptNow(ctx, model.Movie, "r1", 2); !kept || err != nil {
		t.Errorf("tagged: %v, %v", kept, err)
	}
	if _, err := service.KeptNow(ctx, model.Movie, "nobody", 1); err == nil {
		t.Error("an unknown service answered")
	}
	srv.Close()
	if _, err := service.KeptNow(ctx, model.Movie, "r1", 1); err == nil {
		t.Error("an app that's down answered")
	}
}

// A refresh that failed leaves an inventory too old to plan removals on,
// until one succeeds.
func TestFailedRefreshPausesPlanning(t *testing.T) {
	service := New(config.Config{}, nil)
	service.reliability.Inventory = true
	if err := service.fail(errors.New("qBittorrent is down")); err == nil {
		t.Fatal("no error")
	}
	if service.ReliabilitySnapshot().Inventory {
		t.Error("still reliable")
	}
}

// A refresh stopped at its first step, a service out of reach, pauses plans
// like one that fails later (seen with a real qBittorrent stopped).
func TestValidateRefreshPausesPlanning(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	service := New(config.Config{Services: []config.Service{{ID: "q", Type: "qbittorrent", Name: "q", URL: server.URL}}}, nil)
	service.mu.Lock()
	service.reliability.Inventory = true
	service.mu.Unlock()
	if err := service.ValidateRefresh(context.Background()); err == nil {
		t.Fatal("a service out of reach should fail")
	}
	if service.ReliabilitySnapshot().Inventory {
		t.Error("plans should be paused")
	}
}
