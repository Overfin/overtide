package store

import (
	"path/filepath"
	"testing"
	"time"
)

// The removal line is the highest value cleanup plans removed lately:
// dry runs, failures, removals by hand (no value) and older ones don't
// count.
func TestRemovalLine(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	if _, ok, err := db.RemovalLine(now.Add(-30 * 24 * time.Hour)); err != nil || ok {
		t.Fatalf("nothing removed yet: %v %v", ok, err)
	}
	for _, e := range []HistoryEvent{
		{Status: "success", PlanValue: 20},
		{Status: "partial", PlanValue: 24},
		{Status: "success", PlanValue: 90, DryRun: true},
		{Status: "failed", PlanValue: 80},
		{Status: "success"}, // by hand
		{Status: "success", PlanValue: 70, CreatedAt: now.Add(-40 * 24 * time.Hour)},
	} {
		e.EventType = "removal"
		if _, err := db.SaveHistoryEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	if line, ok, err := db.RemovalLine(now.Add(-30 * 24 * time.Hour)); err != nil || !ok || line != 24 {
		t.Errorf("line: %v %v %v", line, ok, err)
	}
}
