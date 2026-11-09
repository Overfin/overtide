package adding

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

func giveUps(changes []string) []string {
	var out []string
	for _, c := range changes {
		if strings.Contains(c, "give up") {
			out = append(out, c)
		}
	}
	return out
}

// A download of Overtide's whose size left hasn't moved for StallGiveUp is
// removed with its torrent and blocklisted; one that moved, a paused one
// and one of a title Overtide doesn't manage are left alone. The wait
// survives a restart: it's kept in the database.
func TestStalledDownloadsAreGivenUp(t *testing.T) {
	now := *arrfake.Day("2026-11-09")
	fake := arrfake.New(now)
	ours := fake.TagID("sonarr", model.TagAdded)
	fake.AddSeries(1, []int{ours}, arrfake.Season(1, 1, 3))
	fake.AddSeries(2, nil, arrfake.Season(1, 1, 3)) // not Overtide's
	fake.AddSeries(3, []int{ours}, arrfake.Season(1, 1, 3))
	fake.Films[5] = map[string]any{"id": 5, "tmdbId": 50, "title": "Dead film", "monitored": false, "hasFile": false, "tags": []int{fake.TagID("radarr", model.TagAdded)}, "added": arrfake.ISO(now)}
	fake.FullQueue = []map[string]any{
		// A season pack, once per episode: given up once.
		{"id": 11, "downloadId": "DEAD", "seriesId": 1, "seasonNumber": 1, "size": 100, "sizeleft": 60, "status": "warning"},
		{"id": 12, "downloadId": "DEAD", "seriesId": 1, "seasonNumber": 1, "size": 100, "sizeleft": 60, "status": "warning"},
		{"id": 21, "downloadId": "OTHER", "seriesId": 2, "seasonNumber": 1, "size": 100, "sizeleft": 60, "status": "warning"},
		{"id": 31, "downloadId": "PAUSED", "seriesId": 3, "seasonNumber": 1, "size": 100, "sizeleft": 60, "status": "paused"},
	}
	fake.RadarrQueue = []map[string]any{
		{"id": 41, "downloadId": "FILM", "movieId": 5, "title": "Dead film", "size": 100, "sizeleft": 90, "status": "downloading"},
	}
	service, cfg := manageTestService(t, fake, config.ManageOwn)
	if err := service.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	service.giveUpStalled(ctx, cfg, false)
	if got := giveUps(fake.Changes); len(got) != 0 {
		t.Fatalf("first sight gives nothing up: %v", got)
	}
	// The film moves a little after a day; the pack doesn't.
	service.now = func() time.Time { return now.Add(24 * time.Hour) }
	fake.RadarrQueue[0]["sizeleft"] = 80
	service.giveUpStalled(ctx, cfg, false)
	// A restart: a new service on the same database keeps the marks.
	restarted := newTestService(t, cfg, now)
	restarted.inv = service.inv
	restarted.now = func() time.Time { return now.Add(StallGiveUp - time.Minute) }
	restarted.giveUpStalled(ctx, cfg, false)
	if got := giveUps(fake.Changes); len(got) != 0 {
		t.Fatalf("before 48 hours: %v", got)
	}
	// Dry run: nothing is removed.
	restarted.now = func() time.Time { return now.Add(StallGiveUp) }
	restarted.giveUpStalled(ctx, cfg, true)
	if got := giveUps(fake.Changes); len(got) != 0 {
		t.Fatalf("dry run: %v", got)
	}
	restarted.giveUpStalled(ctx, cfg, false)
	want := []string{"sonarr give up 11 removeFromClient=true blocklist=true"}
	if got := giveUps(fake.Changes); !slices.Equal(got, want) {
		t.Fatalf("after 48 hours: %v, want %v", got, want)
	}
	// The film, moved a day in, goes 48 hours after it last moved.
	restarted.now = func() time.Time { return now.Add(24*time.Hour + StallGiveUp) }
	restarted.giveUpStalled(ctx, cfg, false)
	want = append(want, "radarr give up 41 removeFromClient=true blocklist=true")
	if got := giveUps(fake.Changes); !slices.Equal(got, want) {
		t.Fatalf("the film: %v, want %v", got, want)
	}
}
