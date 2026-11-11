package httpui

import (
	"slices"
	"testing"

	"github.com/overfin/overtide/internal/store"
)

// Every period of a range is listed, from the one holding its first day.
func TestPeriodStarts(t *testing.T) {
	if got := periodStarts("2026-11-04", "2026-11-17", store.ByWeek); !slices.Equal(got, []string{"2026-11-02", "2026-11-09", "2026-11-16"}) {
		t.Fatalf("weeks: %v", got)
	}
	if got := periodStarts("2026-09-15", "2026-11-10", store.ByMonth); !slices.Equal(got, []string{"2026-09-01", "2026-10-01", "2026-11-01"}) {
		t.Fatalf("months: %v", got)
	}
	if got := periodStarts("2026-11-09", "2026-11-10", store.ByDay); len(got) != 2 {
		t.Fatalf("days: %v", got)
	}
}
