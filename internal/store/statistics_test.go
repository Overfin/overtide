package store

import (
	"path/filepath"
	"testing"
)

// Imports and removals are counted by period; dry runs and failures
// aren't removals; a disk keeps its day's last sample.
func TestStatistics(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range []string{
		`INSERT INTO imported_files (service_id, record_id, kind, source_id, size, origin, imported_at, day) VALUES
			('r', 1, 'movie', 1, 100, '', '2026-11-02T10:00:00Z', '2026-11-02'),
			('s', 2, 'series', 2, 10, 'adding', '2026-11-04T10:00:00Z', '2026-11-04'),
			('s', 3, 'series', 2, 10, '', '2026-11-09T10:00:00Z', '2026-11-09')`,
		`INSERT INTO history_events (event_type, status, dry_run, requested_kind, reclaimable_bytes, created_at) VALUES
			('removal', 'success', 0, 'media', 50, '2026-11-03T10:00:00Z'),
			('removal', 'success', 0, 'torrent', 5, '2026-11-08T10:00:00Z'),
			('removal', 'dry_run', 1, 'media', 999, '2026-11-03T10:00:00Z'),
			('removal', 'failed', 0, 'media', 999, '2026-11-03T10:00:00Z')`,
	} {
		if err := s.exec(q); err != nil {
			t.Fatal(err)
		}
	}
	// Weeks from Monday: Nov 2 and Nov 9, 2026.
	imports, err := s.ImportsSince("2026-11-01", ByWeek)
	if err != nil || len(imports) != 2 || imports[0].Start != "2026-11-02" || imports[0].Films != 1 || imports[0].Episodes != 1 ||
		imports[0].FilmBytes != 100 || imports[0].ByOvertide != 1 || imports[1].Start != "2026-11-09" {
		t.Fatalf("imports: %+v %v", imports, err)
	}
	removals, err := s.RemovalsSince("2026-11-01", ByWeek)
	if err != nil || len(removals) != 1 || removals[0].Titles != 1 || removals[0].Torrents != 1 || removals[0].FreedBytes != 55 {
		t.Fatalf("removals: %+v %v", removals, err)
	}
	if month, _ := s.ImportsSince("2026-01-01", ByMonth); len(month) != 1 || month[0].Start != "2026-11-01" {
		t.Fatalf("by month: %+v", month)
	}
	if first, _ := s.FirstStatisticsDay(); first != "2026-11-02" {
		t.Fatalf("first day: %q", first)
	}
	s.KeepDiskSample(DiskSample{Day: "2026-11-10", Device: "/d", UsedBytes: 1, TotalBytes: 9})
	s.KeepDiskSample(DiskSample{Day: "2026-11-10", Device: "/d", UsedBytes: 2, TotalBytes: 9})
	if got, _ := s.DiskSamplesSince("2026-11-01"); len(got) != 1 || got[0].UsedBytes != 2 {
		t.Fatalf("samples: %+v", got)
	}
}
