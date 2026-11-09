package httpui

import (
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
)

// Without an Overfin, plans propose nothing, not even lone torrents or
// spare copies: planning stays paused however reliable the rest is.
func TestPlanningPausedWithoutOverfin(t *testing.T) {
	ready := inventory.Reliability{Inventory: true, Valuation: true, FileModel: "reliable", Overfin: "not configured"}
	without := &Server{inv: inventory.New(config.Config{}, nil)}
	if without.planningReliable(ready) {
		t.Fatal("planning must stay paused without an Overfin")
	}
	if without.overfinConfigured() {
		t.Fatal("no Overfin URL: not configured")
	}
	with := &Server{inv: inventory.New(config.Config{Overfin: config.Connection{URL: "http://192.0.2.10:8080", APIKey: "k"}}, nil)}
	ready.Overfin = "reliable"
	if !with.planningReliable(ready) {
		t.Fatal("with an Overfin and everything reliable, planning runs")
	}
}
