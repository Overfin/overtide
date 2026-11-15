package httpui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// Before a plan removes titles, Overfin is asked which should wait: a
// held one is skipped, the title taking its place is asked about in turn,
// and the plans shown skip it too, listing it as held. In dry run nothing
// is asked.
func TestHoldDepartures(t *testing.T) {
	var mu sync.Mutex
	var asks [][]string
	until := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Titles []map[string]any `json:"titles"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		var ids []string
		for _, t := range req.Titles {
			ids = append(ids, t["tmdbId"].(string))
			if t["tmdbId"] == "1" {
				t["holdUntil"] = until.Format(time.RFC3339)
			}
		}
		asks = append(asks, ids)
		mu.Unlock()
		json.NewEncoder(w).Encode(req)
	}))
	defer srv.Close()
	cfg := config.Config{}
	cfg.Overfin = config.Connection{URL: srv.URL, APIKey: "ofapp_k"}
	server, _ := libraryServer(t, cfg)

	film := func(id int) cleanup.Action {
		return cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{Type: model.Movie, ServiceID: "r", SourceID: id, TMDBID: id, Title: "Film"}}
	}
	candidates := []cleanup.Action{film(1), film(2), film(3)}
	// The plan takes the first title not left out.
	build := func(excluded map[string]bool) (cleanup.Plan, error) {
		for _, a := range candidates {
			if !excluded[cleanup.ActionKey(a)] {
				return cleanup.Plan{Available: true, Actions: []cleanup.Action{a}}, nil
			}
		}
		return cleanup.Plan{Available: true}, nil
	}
	plan, err := server.holdDepartures(context.Background(), server.inv.Config(), "/disk", build)
	if err != nil || len(plan.Actions) != 1 || plan.Actions[0].Media.TMDBID != 2 {
		t.Fatalf("plan: %+v, %v", plan.Actions, err)
	}
	if len(asks) != 2 || len(asks[0]) != 1 || len(asks[1]) != 2 {
		t.Errorf("asked: %v", asks)
	}
	if held := server.heldOn("/disk"); len(held) != 1 || !held[0].Until.Equal(until) {
		t.Errorf("held: %+v", held)
	}
	if keys := server.heldKeys(nil); !keys[cleanup.ActionKey(film(1))] {
		t.Errorf("plans shown don't skip it: %v", keys)
	}

	// Dry run: nothing asked.
	asks = nil
	dry := server.inv.Config()
	dry.Removal.DryRun = true
	if _, err := server.holdDepartures(context.Background(), dry, "/disk", build); err != nil || len(asks) != 0 {
		t.Errorf("dry run asked: %v, %v", asks, err)
	}
}
