package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
)

// Overfin's scores become the items' Retention Value, and valuation is
// reliable again once they're read; when Overfin fails, automatic removal
// planning stays paused.
func TestRefreshOverfin(t *testing.T) {
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"titles":[{"kind":"movie","tmdbId":"7","score":33}]}`))
	}))
	defer server.Close()

	service := New(config.Config{}, nil)
	service.cfg.Overfin = config.Connection{URL: server.URL, APIKey: "ofapp_k"}
	service.overfin = overfin.New(server.URL, "ofapp_k")
	service.reliability = Reliability{Inventory: true, Overfin: "stale"}
	service.generation = 1
	service.items = []model.Media{{Type: model.Movie, SourceID: 1, TMDBID: 7}, {Type: model.Movie, SourceID: 2, TMDBID: 8}}

	if err := service.RefreshOverfin(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := service.ReliabilitySnapshot()
	if r.Overfin != "reliable" || !r.Valuation {
		t.Errorf("after reading: %+v", r)
	}
	for _, m := range service.items {
		switch m.TMDBID {
		case 7:
			if m.RetentionValue != 33 || m.Protected || m.OverfinEnrichedAt.IsZero() {
				t.Errorf("scored: %+v", m)
			}
		case 8:
			if !m.Protected {
				t.Errorf("unscored, yet not protected: %+v", m)
			}
		}
	}

	fail = true
	if err := service.RefreshOverfin(context.Background()); err == nil {
		t.Fatal("a failure went unnoticed")
	}
	if r := service.ReliabilitySnapshot(); r.Overfin != "stale" || r.Valuation {
		t.Errorf("after a failure: %+v", r)
	}
}

// A base refresh keeps the scores of items whose IDs didn't change.
func TestPreserveOverfinFacts(t *testing.T) {
	score := 50.0
	previous := []model.Media{{Type: model.Series, ServiceID: "s", SourceID: 1, TMDBID: 5, OverfinScore: &score, OverfinSeasonScores: map[int]float64{1: 9}}}
	kept := []model.Media{{Type: model.Series, ServiceID: "s", SourceID: 1, TMDBID: 5}}
	moved := []model.Media{{Type: model.Series, ServiceID: "s", SourceID: 1, TMDBID: 6}}
	preserveOverfinFacts(kept, previous)
	preserveOverfinFacts(moved, previous)
	if kept[0].OverfinSeasonScores[1] != 9 || kept[0].OverfinScore == nil {
		t.Errorf("kept: %+v", kept[0])
	}
	if moved[0].OverfinSeasonScores != nil {
		t.Errorf("an item whose IDs changed kept its scores: %+v", moved[0])
	}
}
