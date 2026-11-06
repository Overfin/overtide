package adding

import (
	"context"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/tries"
)

// The titles a day: past the limit, Overfin's suggestions wait until the
// oldest download turns 24 hours old; a series too big for a whole day
// still goes when nothing else started.
func TestDailyLimit(t *testing.T) {
	now := *arrfake.Day("2026-10-25")
	cfg := config.Config{}
	cfg.Adding.TitlesPerDay = 3
	service := newTestService(t, cfg, now)
	clock := now
	service.now = func() time.Time { return clock }
	if ok, _ := service.DailyLimit(5); !ok {
		t.Fatal("a series bigger than the day, nothing started: held back")
	}
	for _, offset := range []time.Duration{0, time.Hour, 2 * time.Hour} {
		clock = now.Add(offset)
		if err := service.recordStarted(1); err != nil {
			t.Fatal(err)
		}
	}
	clock = now.Add(3 * time.Hour)
	ok, until := service.DailyLimit(1)
	if ok || !until.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("at the limit: %v until %v", ok, until)
	}
	if _, until := service.DailyLimit(2); !until.Equal(now.Add(25 * time.Hour)) {
		t.Fatalf("two more: until %v", until)
	}
	clock = now.Add(24*time.Hour + time.Minute)
	if ok, _ := service.DailyLimit(1); !ok {
		t.Fatal("the oldest is over 24 hours old: still held back")
	}
}

// A run at the day's limit tries no suggestion, but a request goes.
func TestRunHoldsEveryTitleAtTheDailyLimit(t *testing.T) {
	now := *arrfake.Day("2026-10-25")
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr", URL: "http://127.0.0.1:1", APIKey: "k"}}}
	cfg.Adding.Mode, cfg.Adding.TitlesPerDay = config.AddingOn, 1
	service := newTestService(t, cfg, now)
	service.disk = func(config.Config, string, bool) string { return "" }
	if err := service.recordStarted(1); err != nil {
		t.Fatal(err)
	}
	service.inv.Store().SetMeta(tagsKey, "done") // no Overtide tags to replace
	entries := []Entry{
		{Key: movieKey(1), Kind: "movie", ServiceID: "r", ExternalID: 1, Title: "Suggested", Source: FromOverfin, State: Waiting, AddedAt: now},
		{Key: movieKey(2), Kind: "movie", ServiceID: "r", ExternalID: 2, Title: "Asked for", Source: FromRequest, State: Waiting, AddedAt: now},
		{Key: movieKey(3), Kind: "movie", ServiceID: "r", ExternalID: 3, Title: "Found", Source: FromLibrary, State: Waiting, AddedAt: now},
	}
	if err := service.save(entries); err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := service.Entries()
	for _, entry := range after {
		if entry.Result != "" {
			t.Errorf("%s was tried: %q", entry.Title, entry.Result)
		}
	}
	if live := service.Live(); !live.DailyUntil.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("live: %+v", live)
	}
}

// A download counts once per season, wherever it came from; one in dry run
// doesn't count.
func TestDownloadsCounted(t *testing.T) {
	service := newTestService(t, config.Config{}, *arrfake.Day("2026-10-25"))
	a := tries.Title{Monitor: func(int, bool) error { return nil }}
	service.downloading(&Entry{Source: FromOverfin, Kind: "series", Seasons: []int{1, 2}, ArrID: 1}, a, false)
	service.downloading(&Entry{Source: FromRequest, Kind: "movie", ArrID: 2}, a, false)
	service.downloading(&Entry{Source: FromOverfin, Kind: "movie", ArrID: 3}, a, true)
	if times, err := service.started(); err != nil || len(times) != 3 {
		t.Fatalf("counted %d, %v", len(times), err)
	}
}
