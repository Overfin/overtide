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
	actions := func(p cleanup.Plan) []cleanup.Action { return p.Actions }
	plan, cleared, err := server.holdDepartures(context.Background(), server.inv.Config(), "/disk", build, actions)
	if err != nil || len(plan.Actions) != 1 || plan.Actions[0].Media.TMDBID != 2 {
		t.Fatalf("plan: %+v, %v", plan.Actions, err)
	}
	if !cleared[cleanup.ActionKey(film(2))] || cleared[cleanup.ActionKey(film(1))] {
		t.Errorf("cleared: %v", cleared)
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
	if _, _, err := server.holdDepartures(context.Background(), dry, "/disk", build, actions); err != nil || len(asks) != 0 {
		t.Errorf("dry run asked: %v, %v", asks, err)
	}
}

// Automatic removal takes other actions than the plan's first picks (it
// leaves some out, such as torrents it may not touch, and goes further down
// the list): those it takes are the ones asked about, and only those
// asked about (and not held) are cleared to go.
func TestHoldDeparturesAskWhatIsTaken(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Titles []map[string]any `json:"titles"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		for _, t := range req.Titles {
			asked[t["tmdbId"].(string)] = true
			if t["tmdbId"] == "3" {
				t["holdUntil"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
			}
		}
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
	all := []cleanup.Action{film(1), film(2), film(3), film(4)}
	build := func(excluded map[string]bool) (cleanup.Plan, error) {
		var left []cleanup.Action
		for _, a := range all {
			if !excluded[cleanup.ActionKey(a)] {
				left = append(left, a)
			}
		}
		return cleanup.Plan{Available: true, Actions: left[:1], Ordered: left}, nil
	}
	// The plan picks film 1; removal may not take it and takes the next two.
	take := func(p cleanup.Plan) []cleanup.Action {
		var out []cleanup.Action
		for _, a := range p.Ordered {
			if a.Media.TMDBID != 1 && len(out) < 2 {
				out = append(out, a)
			}
		}
		return out
	}
	plan, cleared, err := server.holdDepartures(context.Background(), server.inv.Config(), "/disk", build, take)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !asked["2"] || !asked["3"] || !asked["4"] {
		t.Errorf("not asked about what's taken: %v", asked)
	}
	var taken []int
	for _, a := range take(plan) {
		if cleared[cleanup.ActionKey(a)] {
			taken = append(taken, a.Media.TMDBID)
		}
	}
	if len(taken) != 2 || taken[0] != 2 || taken[1] != 4 {
		t.Errorf("taken: %v (3 is held, 1 not taken)", taken)
	}
}

// When Overfin can't answer, removal still goes on: only the holds known
// stay out (the plan was built without them).
func TestHoldDeparturesOverfinDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	cfg := config.Config{}
	cfg.Overfin = config.Connection{URL: srv.URL, APIKey: "ofapp_k"}
	server, _ := libraryServer(t, cfg)
	film := cleanup.Action{Kind: cleanup.StandaloneMedia, Media: model.Media{Type: model.Movie, ServiceID: "r", SourceID: 7, TMDBID: 7, Title: "Film"}}
	build := func(map[string]bool) (cleanup.Plan, error) {
		return cleanup.Plan{Available: true, Actions: []cleanup.Action{film}, Ordered: []cleanup.Action{film}}, nil
	}
	_, cleared, err := server.holdDepartures(context.Background(), server.inv.Config(), "/disk", build, func(p cleanup.Plan) []cleanup.Action { return p.Actions })
	if err != nil || !cleared[cleanup.ActionKey(film)] {
		t.Errorf("Overfin down: cleared %v, %v", cleared, err)
	}
}
