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
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/sonarrdriver"
)

// Seasons just finished don't wait for the titles a day but count in it;
// older ones wait, most valued first.
func TestSeasonsAndTheDailyLimit(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	pack := func(guid string, seasonNumber int) []map[string]any {
		return []map[string]any{{"guid": guid, "indexerId": 1, "title": guid, "fullSeason": true, "seasonNumber": seasonNumber, "downloadAllowed": true}}
	}
	fake.AddSeries(1, nil, arrfake.Season(1, 1, 2))
	fake.AddSeries(2, nil, arrfake.Season(2, 1, 2), arrfake.Season(3, 1, 2))
	fake.AddEpisode(1, 1, 1, now.AddDate(0, 0, -10), false)
	fake.AddEpisode(2, 2, 2, now.AddDate(-1, 0, 0), false)
	fake.AddEpisode(3, 2, 3, now.AddDate(-1, 0, 0), false)
	fake.Releases[1], fake.Releases[2], fake.Releases[3] = pack("fresh", 1), pack("less", 2), pack("more", 3)
	service, cfg := manageTestService(t, fake, config.ManageAll)
	cfg.Adding.TitlesPerDay = 2
	if err := service.inv.SetAdding(config.AddingOn, config.ManageAll, 5, 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := service.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.values = &overfin.Values{Seasons: map[int]map[int]float64{1002: {2: 1, 3: 9}}}
	if err := service.recordStarted(2); err != nil { // the limit's reached
		t.Fatal(err)
	}
	wanted := map[int]sonarrdriver.EpisodeWant{1: {SeriesID: 1, Season: 1}, 2: {SeriesID: 2, Season: 2}, 3: {SeriesID: 2, Season: 3}}
	if err := service.sonarr.SaveStates(map[string]*sonarrdriver.State{"s": {Wanted: wanted}}); err != nil {
		t.Fatal(err)
	}
	if used, err := service.replaceSeasons(context.Background(), cfg, 5, false, false); err != nil || used != 0 {
		t.Fatalf("older seasons past the limit: %d, %v", used, err)
	}
	used, err := service.replaceSeasons(context.Background(), cfg, 5, false, true)
	if err != nil || used != 1 || !slices.Equal(fake.Searches(), []string{"grab fresh"}) {
		t.Fatalf("the season just finished: %d, %v, %v", used, err, fake.Searches())
	}
	if times, _ := service.started(); len(times) != 3 {
		t.Errorf("counted %d", len(times))
	}
	// A day later there's room for one again: the most valued.
	service.now = func() time.Time { return now.Add(25 * time.Hour) }
	if err := service.recordStarted(1); err != nil {
		t.Fatal(err)
	}
	used, err = service.replaceSeasons(context.Background(), cfg, 5, false, false)
	if err != nil || used != 1 || !slices.Equal(fake.Searches(), []string{"grab fresh", "grab more"}) {
		t.Fatalf("after a day: %d, %v, %v", used, err, fake.Searches())
	}
}

// One run's order: an airing episode, then a wish, with the searches left.
func TestRunSearchesAiringEpisodesBeforeWishes(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	fake.AddSeries(1, nil, arrfake.Season(1, 1, 3))
	fake.AddEpisode(1, 1, 1, now.AddDate(0, 0, -3), false)
	fake.AddEpisode(2, 1, 1, now.AddDate(0, 0, 4), false)
	service, _ := manageTestService(t, fake, config.ManageAll)
	if err := service.inv.SetAdding(config.AddingOn, config.ManageAll, 1, 10, 0); err != nil {
		t.Fatal(err)
	}
	service.inv.Store().SetMeta(tagsKey, "done")
	entries := []Entry{{Key: movieKey(9), Kind: "movie", ServiceID: "r", ExternalID: 9, Title: "Wish", Source: FromRequest, State: Waiting, AddedAt: now}}
	if err := service.save(entries); err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fake.Searches(); !slices.Equal(got, []string{"search episode 1"}) {
		t.Errorf("searched: %v", got)
	}
	after, _ := service.Entries()
	if len(after) != 1 || after[0].Result != "" {
		t.Errorf("the wish was tried with no search left: %+v", after)
	}
}

// Upgrades are off unless set: most valued first, within their own limit,
// each monitored only for its search, and not searched again for a while.
func TestSearchUpgrades(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	old := now.AddDate(-1, 0, 0)
	fake.Films[1] = map[string]any{"id": 1, "tmdbId": 11, "title": "Low", "monitored": false, "hasFile": true, "tags": []int{}, "added": arrfake.ISO(old)}
	fake.Films[2] = map[string]any{"id": 2, "tmdbId": 12, "title": "High", "monitored": false, "hasFile": true, "tags": []int{}, "added": arrfake.ISO(old)}
	fake.Films[3] = map[string]any{"id": 3, "tmdbId": 13, "title": "Ignored", "monitored": false, "hasFile": true, "tags": []int{fake.TagID("radarr", model.TagIgnore)}, "added": arrfake.ISO(old)}
	fake.AddSeries(5, nil, arrfake.Season(1, 2, 2), arrfake.Season(2, 1, 3))
	fake.AddEpisode(51, 5, 1, old, true)
	fake.AddEpisode(52, 5, 1, old, true)
	fake.AddEpisode(53, 5, 2, now.AddDate(0, 0, -1), true) // still airing
	fake.AddEpisode(54, 5, 2, now.AddDate(0, 0, 7), false)
	fake.Cutoff["radarr"] = []map[string]any{{"id": 1}, {"id": 2}, {"id": 3}}
	fake.Cutoff["sonarr"] = []map[string]any{{"seriesId": 5, "seasonNumber": 1}, {"seriesId": 5, "seasonNumber": 1}, {"seriesId": 5, "seasonNumber": 2}}
	service, cfg := manageTestService(t, fake, config.ManageAll)
	if err := service.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.values = &overfin.Values{Films: map[int]float64{11: 1, 12: 9, 13: 99}, Seasons: map[int]map[int]float64{1005: {1: 5, 2: 50}}}

	if used, err := service.searchUpgrades(context.Background(), cfg, nil, 5, false); err != nil || used != 0 {
		t.Fatalf("off by default: %d, %v", used, err)
	}
	cfg.Adding.UpgradesPerDay = 2
	if used, err := service.searchUpgrades(context.Background(), cfg, nil, 5, true); err != nil || used != 0 || len(fake.Mutations()) != 0 {
		t.Fatalf("dry run: %d, %v, %v", used, err, fake.Mutations())
	}
	used, err := service.searchUpgrades(context.Background(), cfg, nil, 5, false)
	if err != nil || used != 2 {
		t.Fatalf("%d searches, %v", used, err)
	}
	if got := fake.Searches(); !slices.Equal(got, []string{"search film [2]", "search season 5/1"}) {
		t.Errorf("searched: %v", got)
	}
	if fake.Films[2]["monitored"] != false || fake.Eps[51]["monitored"] != false {
		t.Errorf("left monitored: %v, %v", fake.Films[2], fake.Eps[51])
	}
	if !slices.Contains(fake.Mutations(), "film 2 monitored true") {
		t.Errorf("not monitored for its search: %v", fake.Mutations())
	}
	// The limit's reached.
	if used, _ := service.searchUpgrades(context.Background(), cfg, nil, 5, false); used != 0 {
		t.Errorf("past the limit: %d", used)
	}
	// A day later, the next one; not those searched already.
	service.now = func() time.Time { return now.Add(25 * time.Hour) }
	if used, _ := service.searchUpgrades(context.Background(), cfg, nil, 5, false); used != 1 {
		t.Errorf("the next day: %d", used)
	}
	if got := fake.Searches(); !slices.Equal(got, []string{"search film [2]", "search season 5/1", "search film [1]"}) {
		t.Errorf("searched: %v", got)
	}
	// Later, the season that was airing is out, and the most valued.
	service.now = func() time.Time { return now.Add(UpgradeAgain + 2*24*time.Hour) }
	if used, _ := service.searchUpgrades(context.Background(), cfg, nil, 1, false); used != 1 {
		t.Errorf("after a while: %d", used)
	}
	if got := fake.Searches(); got[len(got)-1] != "search season 5/2" {
		t.Errorf("searched: %v", got)
	}
	// Then, what was searched long enough ago: the most valued film.
	service.now = func() time.Time { return now.Add(UpgradeAgain + 4*24*time.Hour) }
	if err := service.inv.Store().SetMeta(upgradesKey, ""); err != nil {
		t.Fatal(err)
	}
	if used, _ := service.searchUpgrades(context.Background(), cfg, nil, 1, false); used != 1 {
		t.Errorf("searched long ago: %d", used)
	}
	if got := fake.Searches(); got[len(got)-1] != "search film [2]" {
		t.Errorf("searched: %v", got)
	}
}

// Episodes just aired take at most half of a run's searches, so that a wish
// gets some; if nothing else wants them, they get the rest.
func TestRunKeepsSearchesForWishes(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	run := func(t *testing.T, wish bool) []string {
		fake := arrfake.New(now)
		fake.AddSeries(1, nil, arrfake.Season(1, 1, 7))
		for id := 1; id <= 6; id++ {
			fake.AddEpisode(id, 1, 1, now.AddDate(0, 0, -3), false)
		}
		fake.AddEpisode(7, 1, 1, now.AddDate(0, 0, 4), false)
		fake.Films[9] = map[string]any{"id": 9, "tmdbId": 90, "title": "Wish", "monitored": false, "hasFile": false, "tags": []int{}, "added": arrfake.ISO(now), "status": "released", "isAvailable": true}
		service, _ := manageTestService(t, fake, config.ManageAll)
		if err := service.inv.SetAdding(config.AddingOn, config.ManageAll, 4, 10, 0); err != nil {
			t.Fatal(err)
		}
		if err := service.inv.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		service.inv.Store().SetMeta(tagsKey, "done")
		if wish {
			entries := []Entry{{Key: movieKey(90), Kind: "movie", ServiceID: "r", ExternalID: 90, ArrID: 9, Title: "Wish", Source: FromRequest, State: Waiting, AddedAt: now}}
			if err := service.save(entries); err != nil {
				t.Fatal(err)
			}
		}
		if err := service.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		return fake.Searches()
	}
	if got := run(t, false); len(got) != 4 {
		t.Errorf("nothing else to do: %v", got)
	}
	got := run(t, true)
	episodes := 0
	for _, g := range got {
		if strings.HasPrefix(g, "search episode") {
			episodes++
		}
	}
	if episodes > 3 || !slices.Contains(got, "search film [9]") {
		t.Errorf("with a wish: %v", got)
	}
}

// What's downloading comes from the queues, whatever started it: a season
// adds up its episodes; not what Overtide doesn't manage.
func TestDownloadsFromTheQueues(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	ours := fake.TagID("sonarr", model.TagAdded)
	fake.AddSeries(1, []int{ours}, arrfake.Season(1, 1, 3))
	fake.AddSeries(2, nil, arrfake.Season(1, 1, 3))
	fake.Films[5] = map[string]any{"id": 5, "tmdbId": 50, "title": "Gone", "monitored": false, "hasFile": false, "tags": []int{fake.TagID("radarr", model.TagAdded)}, "added": arrfake.ISO(now)}
	fake.FullQueue = []map[string]any{
		{"seriesId": 1, "seasonNumber": 1, "size": 100, "sizeleft": 50, "status": "downloading"},
		{"seriesId": 1, "seasonNumber": 1, "size": 100, "sizeleft": 0, "status": "completed", "trackedDownloadState": "importPending"},
		{"seriesId": 2, "seasonNumber": 1, "size": 100, "sizeleft": 0, "status": "downloading"},
	}
	service, _ := manageTestService(t, fake, config.ManageOwn)
	if err := service.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{Key: movieKey(50), Kind: "movie", ServiceID: "r", ArrID: 5, Title: "Gone", State: Downloading}}
	if err := service.save(entries); err != nil {
		t.Fatal(err)
	}
	downloads, err := service.Downloads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(downloads) != 2 {
		t.Fatalf("downloads: %+v", downloads)
	}
	d := downloads[0]
	if d.Title != "Series 1" || d.Season != 1 || d.Episodes != 2 || d.Progress != 0.75 || d.Status != "Waiting to be imported" || !d.Queued {
		t.Errorf("season: %+v", d)
	}
	if g := downloads[1]; g.Title != "Gone" || g.Queued {
		t.Errorf("left the queue: %+v", g)
	}
}
