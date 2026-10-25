package inventory

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// TestNewTrustsCachedScoresAfterRestart: a process restart that loads
// persisted media trusts the Overfin scores saved with it, rather than
// marking them stale and pausing automatic removal planning until the
// next read.
func TestNewTrustsCachedScoresAfterRestart(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	score := 30.0
	media := []model.Media{{Type: model.Movie, SourceID: 1, Title: "Old Movie", TMDBID: 42, OverfinScore: &score, OverfinEnrichedAt: time.Now().Add(-time.Hour)}}
	if err := database.SaveMedia(media); err != nil {
		t.Fatal(err)
	}

	var cfg config.Config
	cfg.Overfin.URL = "http://overfin.example.org"

	service := New(cfg, database)

	if service.reliability.Overfin != "reliable" {
		t.Fatalf("expected Overfin reliability to trust the just-loaded cache, got %q", service.reliability.Overfin)
	}
	if !service.reliability.Valuation {
		t.Fatalf("expected Valuation to be reliable immediately after a successful cache load, got false (message=%q)", service.reliability.Message)
	}
}
