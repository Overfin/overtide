package sonarrdriver

import (
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// A whole-season pack is never of a lower quality than the season's worst
// file (one release for the season is worth less than the best of its
// files, never less than the worst); single episodes, and a season without
// files, aren't held to it.
func TestPackNotBelowSeason(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	q := func(id int) map[string]any { return map[string]any{"quality": map[string]any{"id": id}} }
	// Least preferred first: SDTV, HDTV-720p, a WEB group (720p, 1080p), Bluray-1080p.
	fake.ProfileItems = []any{map[string]any{"quality": map[string]any{"id": 1}}, map[string]any{"quality": map[string]any{"id": 4}},
		map[string]any{"name": "WEB", "items": []any{map[string]any{"quality": map[string]any{"id": 5}}, map[string]any{"quality": map[string]any{"id": 3}}}},
		map[string]any{"quality": map[string]any{"id": 7}}}
	fake.Series[5] = map[string]any{"id": 5, "qualityProfileId": 1, "seasons": []any{}}
	fake.Files[11] = map[string]any{"id": 11, "seriesId": 5, "seasonNumber": 1, "quality": q(4)} // HDTV: the worst
	fake.Files[12] = map[string]any{"id": 12, "seriesId": 5, "seasonNumber": 1, "quality": q(7)} // Bluray
	driver, _ := testDriver(t, fake, config.ManageAll)
	client, _ := driver.inv.SonarrClient("s")
	release := func(title string, full bool, quality int) sonarr.Release {
		r := sonarr.Release{Title: title, FullSeason: full}
		r.Quality.Quality.ID = quality
		return r
	}
	got := driver.notBelowSeason(client, &SeasonPack{SeriesID: 5, Season: 1},
		[]sonarr.Release{release("sd pack", true, 1), release("web pack", true, 5), release("hdtv pack", true, 4), release("sd episode", false, 1), release("unknown pack", true, 99)})
	var titles []string
	for _, r := range got {
		titles = append(titles, r.Title)
	}
	if len(titles) != 3 || titles[0] != "web pack" || titles[1] != "hdtv pack" || titles[2] != "sd episode" {
		t.Fatalf("kept %v", titles)
	}
	// The profile not read: no pack this time, the episodes stay.
	fake.Series[6] = map[string]any{"id": 6, "qualityProfileId": 1, "seasons": []any{}}
	fake.Files[13] = map[string]any{"id": 13, "seriesId": 6, "seasonNumber": 1, "quality": q(4)}
	saved := fake.ProfileItems
	fake.ProfileItems = nil
	if got := driver.notBelowSeason(client, &SeasonPack{SeriesID: 6, Season: 1}, []sonarr.Release{release("pack", true, 7), release("episode", false, 1)}); len(got) != 1 || got[0].Title != "episode" {
		t.Fatalf("profile not read: %v", got)
	}
	fake.ProfileItems = saved
	// A season without files: nothing to hold it to.
	if got := driver.notBelowSeason(client, &SeasonPack{SeriesID: 5, Season: 2}, []sonarr.Release{release("sd pack", true, 1)}); len(got) != 1 {
		t.Fatalf("no files: %v", got)
	}
}

// A season that just finished and came complete episode by episode waits
// for its pack as an upgrade; one still missing an episode, or long
// finished, doesn't.
func TestSeasonCompleted(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	ep := func(id, season int, aired time.Time, file bool) {
		fake.Eps[id] = map[string]any{"id": id, "seriesId": 5, "seasonNumber": season, "episodeNumber": id, "airDateUtc": arrfake.ISO(aired), "hasFile": file}
	}
	ep(1, 1, now.AddDate(0, 0, -20), true)
	ep(2, 1, now.AddDate(0, 0, -6), true) // its last, just came
	ep(3, 2, now.AddDate(0, 0, -6), false)
	ep(4, 3, now.AddDate(-1, 0, 0), true)
	driver, _ := testDriver(t, fake, config.ManageAll)
	client, _ := driver.inv.SonarrClient("s")
	state := stateFor(map[string]*State{}, "s")
	for _, season := range []int{1, 2, 3} {
		driver.seasonCompleted(client, state, 5, season)
	}
	if pack := state.Seasons[seasonKey(5, 1)]; pack == nil || !pack.Upgrade || pack.pending() {
		t.Fatalf("season 1: %+v", pack)
	}
	if state.Seasons[seasonKey(5, 2)] != nil || state.Seasons[seasonKey(5, 3)] != nil {
		t.Fatalf("seasons 2, 3: %+v", state.Seasons)
	}
}
