package adding

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/radarr"
	"github.com/overfin/overtide/internal/services/sonarr"
)

func day(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

func TestMovieReleased(t *testing.T) {
	now := *day("2026-10-25")
	cases := []struct {
		name     string
		lookup   radarr.MovieLookup
		released bool
		next     *time.Time
	}{
		{"digital release passed", radarr.MovieLookup{DigitalRelease: day("2026-09-01")}, true, nil},
		{"digital release ahead", radarr.MovieLookup{DigitalRelease: day("2026-12-01"), InCinemas: day("2026-09-01")}, false, day("2026-12-01")},
		{"disc before digital", radarr.MovieLookup{DigitalRelease: day("2026-12-01"), PhysicalRelease: day("2026-10-01")}, true, nil},
		{"cinemas only, long ago", radarr.MovieLookup{InCinemas: day("2026-01-01")}, true, nil},
		{"cinemas only, lately", radarr.MovieLookup{InCinemas: day("2026-10-01")}, false, day("2026-12-30")},
		{"no date, said released", radarr.MovieLookup{Status: "released"}, true, nil},
	}
	for _, tc := range cases {
		released, next := movieReleased(tc.lookup, now)
		if released != tc.released || (tc.next != nil && !next.Equal(*tc.next)) {
			t.Errorf("%s: got %v %v", tc.name, released, next)
		}
	}
	if released, next := movieReleased(radarr.MovieLookup{Status: "announced"}, now); released || !next.Equal(now.Add(7*24*time.Hour)) {
		t.Errorf("nothing known: got %v %v", released, next)
	}
}

func TestSeriesReleased(t *testing.T) {
	now := *day("2026-10-25")
	if released, next := seriesReleased(sonarr.SeriesLookup{Status: "upcoming", FirstAired: day("2027-01-10")}, now); released || !next.Equal(*day("2027-01-10")) {
		t.Errorf("upcoming with a date: %v %v", released, next)
	}
	if released, _ := seriesReleased(sonarr.SeriesLookup{Status: "upcoming"}, now); released {
		t.Error("upcoming without a date counted as out")
	}
	if released, _ := seriesReleased(sonarr.SeriesLookup{Status: "continuing", FirstAired: day("2025-03-01")}, now); !released {
		t.Error("an airing series counted as not out")
	}
}

func TestRetryDelay(t *testing.T) {
	want := []int{1, 2, 4, 8, 16, 30, 30}
	for i, days := range want {
		if got := retryDelay(i + 1); got != time.Duration(days)*24*time.Hour {
			t.Errorf("try %d: %s, want %d days", i+1, got, days)
		}
	}
}

// Requests first, then Overfin's suggestions, each in its order.
func TestSortForTries(t *testing.T) {
	entries := []Entry{
		{Key: "overfin-2", Source: FromOverfin, Position: 2},
		{Key: "request-1", Source: FromRequest, Position: 1},
		{Key: "overfin-1", Source: FromOverfin, Position: 1},
		{Key: "request-0", Source: FromRequest},
	}
	sortForTries(entries)
	got := ""
	for _, entry := range entries {
		got += entry.Key + " "
	}
	if got != "request-0 request-1 overfin-1 overfin-2 " {
		t.Fatal(got)
	}
}

func TestSearchAndCheck(t *testing.T) {
	pollEvery = time.Millisecond
	statuses := []string{"queued", "started", "completed"}
	status := func(int) (string, error) {
		next := statuses[0]
		if len(statuses) > 1 {
			statuses = statuses[1:]
		}
		return next, nil
	}
	grabbed, err := searchAndCheck(context.Background(), func() (int, error) { return 7, nil }, status, func() (bool, error) { return true, nil })
	if err != nil || !grabbed {
		t.Fatalf("got %v %v", grabbed, err)
	}
	failed := func(int) (string, error) { return "failed", nil }
	if _, err := searchAndCheck(context.Background(), func() (int, error) { return 7, nil }, failed, func() (bool, error) { return true, nil }); err == nil {
		t.Fatal("a failed search went through")
	}
}

// A film is one search, a series one per season.
func TestEntrySearches(t *testing.T) {
	if n := (Entry{Kind: "movie"}).searches(); n != 1 {
		t.Errorf("film: %d", n)
	}
	if n := (Entry{Kind: "series", Seasons: []int{1, 2, 3, 4}}).searches(); n != 4 {
		t.Errorf("series: %d", n)
	}
}

// Clearing takes out what waits, but for requests and the titles taken
// from Radarr; what's moving stays.
func TestClear(t *testing.T) {
	service := newTestService(t, config.Config{}, *day("2026-10-30"))
	entries := []Entry{
		{Key: movieKey(1), ServiceID: "r", Source: FromOverfin, State: Waiting},
		{Key: movieKey(2), ServiceID: "r", Source: FromRequest, State: Waiting},
		{Key: movieKey(3), ServiceID: "r", Source: FromLibrary, State: Waiting},
		{Key: movieKey(4), ServiceID: "r", Source: FromOverfin, State: Downloading},
		{Key: movieKey(5), ServiceID: "r", Source: FromOverfin, State: NotReleased},
		{Key: movieKey(6), ServiceID: "r", Source: FromOverfin, State: NotReleased, MonitoredAt: *day("2026-10-29")},
	}
	if err := service.save(entries); err != nil {
		t.Fatal(err)
	}
	cleared, err := service.Clear()
	if err != nil || cleared != 2 {
		t.Fatalf("cleared %d, %v", cleared, err)
	}
	left, _ := service.load()
	var keys []string
	for _, e := range left {
		keys = append(keys, e.Key)
	}
	if strings.Join(keys, " ") != "movie:2 movie:3 movie:4 movie:6" {
		t.Fatalf("left: %v", keys)
	}
}
