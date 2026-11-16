package adding

import (
	"context"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// Adding switched off unmonitors, once, what Overtide monitored and has no
// file yet: a film waiting for its release, its series' episodes airing.
// Radarr's and Sonarr's RSS would grab them otherwise. Dry run changes
// nothing in either.
func TestAddingOffUnmonitorsWhatOvertideMonitored(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	build := func(mode string) (*arrfake.Arrs, *Service) {
		fake := arrfake.New(now)
		tag := fake.TagID("sonarr", model.TagAdded)
		fake.Films[42] = map[string]any{"id": 42, "tmdbId": 5, "title": "Coming", "monitored": true, "hasFile": false}
		fake.Series[7] = map[string]any{"id": 7, "tvdbId": 70, "title": "Airing", "path": "/data/Series/Airing", "monitored": true, "tags": []int{tag}, "seasons": []any{}}
		fake.Series[8] = map[string]any{"id": 8, "tvdbId": 80, "title": "Not ours", "path": "/data/Series/Other", "monitored": true, "tags": []int{}, "seasons": []any{}}
		fake.Eps[1] = map[string]any{"id": 1, "seriesId": 7, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": arrfake.ISO(now.Add(time.Hour)), "hasFile": false, "monitored": true}
		fake.Eps[2] = map[string]any{"id": 2, "seriesId": 8, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": arrfake.ISO(now.Add(time.Hour)), "hasFile": false, "monitored": true}
		cfg := arrfake.Config(t, fake, config.ManageOwn)
		cfg.Adding.Mode = mode
		service := newTestService(t, cfg, now)
		service.disk = func(config.Config, string, bool) string { return "" }
		entries := []Entry{{Key: movieKey(5), Kind: "movie", ServiceID: "r", ExternalID: 5, ArrID: 42, Title: "Coming", Source: FromOverfin,
			State: NotReleased, MonitoredAt: now.Add(-time.Hour), ReleaseAt: now.Add(24 * time.Hour), Counted: true}}
		if err := service.save(entries); err != nil {
			t.Fatal(err)
		}
		return fake, service
	}

	fake, service := build(config.AddingDryRun)
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.Films[42]["monitored"] != true || fake.Eps[1]["monitored"] != true {
		t.Fatalf("dry run unmonitored: film %v, episode %v", fake.Films[42]["monitored"], fake.Eps[1]["monitored"])
	}

	fake, service = build(config.AddingOff)
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.Films[42]["monitored"] != false || fake.Eps[1]["monitored"] != false {
		t.Fatalf("off: film %v, episode %v", fake.Films[42]["monitored"], fake.Eps[1]["monitored"])
	}
	if fake.Eps[2]["monitored"] != true {
		t.Error("another series' episode unmonitored")
	}
	entries, _ := service.Entries()
	if len(entries) != 1 || !entries[0].waiting() || !entries[0].MonitoredAt.IsZero() {
		t.Fatalf("the film's entry: %+v", entries)
	}
	// Once: what's monitored since, by hand, stays.
	fake.Eps[1]["monitored"] = true
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fake.Eps[1]["monitored"] != true {
		t.Error("unmonitored again on the next run")
	}
}
