package adding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/store"

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

// Requests first, then Overfin's picks, then lists, each in its order.
func TestSortForTries(t *testing.T) {
	entries := []Entry{
		{Key: "list-2", Source: FromList, From: "A", Position: 2},
		{Key: "overfin", Source: FromOverfin},
		{Key: "list-1", Source: FromList, From: "A", Position: 1},
		{Key: "request", Source: FromRequest},
	}
	sortForTries(entries)
	got := ""
	for _, entry := range entries {
		got += entry.Key + " "
	}
	if got != "request overfin list-1 list-2 " {
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

func TestSettle(t *testing.T) {
	service := &Service{now: func() time.Time { return *day("2026-10-25") }}
	removed := 0
	remove := func() error { removed++; return nil }

	entry := Entry{ArrID: 4}
	service.settle(&entry, true, nil, remove)
	if entry.State != Downloading || removed != 0 {
		t.Fatalf("grabbed: %#v, removed %d", entry, removed)
	}

	entry = Entry{ArrID: 4, Tries: 1}
	service.settle(&entry, false, nil, remove)
	if entry.State != Waiting || entry.ArrID != 0 || entry.Tries != 2 || removed != 1 || !entry.NextTry.Equal(day("2026-10-25").Add(2*24*time.Hour)) {
		t.Fatalf("nothing found: %#v, removed %d", entry, removed)
	}

	entry = Entry{ArrID: 4}
	service.settle(&entry, false, errors.New("the search failed"), remove)
	if entry.ArrID != 0 || entry.Tries != 0 || removed != 2 {
		t.Fatalf("search error: %#v", entry)
	}

	entry = Entry{ArrID: 4}
	service.settle(&entry, false, nil, func() error { return errors.New("down") })
	if entry.ArrID != 4 {
		t.Fatalf("a title that couldn't be taken out lost its ID: %#v", entry)
	}
}

// Handing everything over takes only the titles without files that aren't
// Overtide's.
func TestAdoptAll(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := New(inventory.New(config.Config{}, database))
	if err := service.save([]Entry{{Key: "movie:1", State: WantedElsewhere}, {Key: "movie:2", State: InLibrary}, {Key: "movie:3", State: WantedElsewhere}}); err != nil {
		t.Fatal(err)
	}
	if err := service.Adopt(""); err != nil {
		t.Fatal(err)
	}
	entries, _ := service.Entries()
	for _, entry := range entries {
		handed := entry.Ours && entry.State == Waiting
		if handed != (entry.Key != "movie:2") {
			t.Errorf("%s: %#v", entry.Key, entry)
		}
	}
	if err := service.Adopt(""); err == nil {
		t.Error("nothing left to hand over, yet no error")
	}
}
