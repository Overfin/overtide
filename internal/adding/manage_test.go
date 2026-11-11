package adding

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/sonarrdriver"
	"github.com/overfin/overtide/internal/tries"
)

func manageTestService(t *testing.T, fake *arrfake.Arrs, manage string) (*Service, config.Config) {
	t.Helper()
	cfg := arrfake.Config(t, fake, manage)
	service := newTestService(t, cfg, fake.Now)
	service.disk = func(config.Config, string, bool) string { return "" }
	tries.PollEvery = time.Millisecond
	return service, cfg
}

// The former tags go, once: "overtide" becomes the tag of where the title
// came from (overtide-found when it isn't in the waiting list), Overswell's
// their Overtide counterpart; other tags stay.
func TestRetagReplacesTheFormerTags(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	legacy := fake.TagID("radarr", "overtide")
	for id := 1; id <= 4; id++ {
		fake.Films[id] = map[string]any{"id": id, "tmdbId": id, "tags": []int{legacy}, "monitored": true, "hasFile": true}
	}
	fake.Films[4]["tags"] = []int{} // never managed
	fake.Films[5] = map[string]any{"id": 5, "tmdbId": 5, "tags": []int{fake.TagID("radarr", "overswell-new"), fake.TagID("radarr", "overtide-keep")}, "monitored": true, "hasFile": true}
	fake.Films[6] = map[string]any{"id": 6, "tmdbId": 6, "tags": []int{fake.TagID("radarr", "overswell-ignore")}, "monitored": true, "hasFile": true}
	service, cfg := manageTestService(t, fake, config.ManageOwn)
	entries := []Entry{
		{Key: movieKey(1), Kind: model.Movie, ServiceID: "r", Source: FromOverfin, ArrID: 1},
		{Key: movieKey(2), Kind: model.Movie, ServiceID: "r", Source: FromRequest, ArrID: 2},
	}
	if err := service.retag(context.Background(), cfg, entries); err != nil {
		t.Fatal(err)
	}
	want := map[int][]string{1: {model.TagAdded}, 2: {model.TagRequested}, 3: {model.TagFound}, 4: nil, 5: {model.TagFound, "overtide-keep"}, 6: {model.TagIgnore}}
	for id, labels := range want {
		got := fake.Labels("radarr", fake.Films[id]["tags"])
		slices.Sort(got)
		slices.Sort(labels)
		if !slices.Equal(got, labels) {
			t.Errorf("film %d: %v, want %v", id, got, labels)
		}
	}
	before := len(fake.Mutations())
	if err := service.retag(context.Background(), cfg, entries); err != nil {
		t.Fatal(err)
	}
	if after := len(fake.Mutations()); after != before {
		t.Errorf("retagged again: %v", fake.Mutations()[before:])
	}
}

// "Manage all" takes every title, and keeps what Radarr and Sonarr wanted:
// a monitored film without its file goes in line, a removed one (not
// monitored) stays unwanted; a followed series' recent episodes are
// monitored for the RSS, older missing ones wanted; an unfollowed series
// gets nothing new. Dry run changes nothing.
func TestTakeAll(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	season := func(n int, monitored bool) map[string]any {
		return map[string]any{"seasonNumber": n, "monitored": monitored}
	}
	build := func() *arrfake.Arrs {
		fake := arrfake.New(now)
		fake.Films[1] = map[string]any{"id": 1, "tmdbId": 11, "title": "Has its file", "monitored": true, "hasFile": true, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
		fake.Films[2] = map[string]any{"id": 2, "tmdbId": 12, "title": "Wanted", "monitored": true, "hasFile": false, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
		fake.Films[3] = map[string]any{"id": 3, "tmdbId": 13, "title": "Removed", "monitored": false, "hasFile": false, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
		fake.Series[1] = map[string]any{"id": 1, "title": "Airing", "monitored": true, "monitorNewItems": "all", "seasons": []any{season(1, true), season(2, true)}, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
		fake.Series[2] = map[string]any{"id": 2, "title": "Dropped", "monitored": true, "monitorNewItems": "none", "seasons": []any{season(1, false)}, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
		ep := func(id, series, season int, aired time.Time, file, monitored bool) {
			fake.Eps[id] = map[string]any{"id": id, "seriesId": series, "seasonNumber": season, "episodeNumber": id, "airDateUtc": arrfake.ISO(aired), "hasFile": file, "monitored": monitored}
		}
		ep(101, 1, 1, now.AddDate(0, 0, -60), false, true)  // old, missing, wanted
		ep(102, 1, 2, now.AddDate(0, 0, -3), false, true)   // recent, missing: the window's
		ep(103, 1, 2, now.Add(24*time.Hour), false, true)   // tomorrow: the window's
		ep(104, 1, 2, now.AddDate(0, 0, -1), true, true)    // has its file
		ep(105, 1, 2, now.AddDate(0, 0, 30), false, true)   // later: not yet
		ep(201, 2, 1, now.AddDate(0, 0, -2), false, false)  // unfollowed series
		ep(106, 1, 1, now.AddDate(0, 0, -40), false, false) // unmonitored: unwanted
		return fake
	}

	fake := build()
	service, cfg := manageTestService(t, fake, config.ManageAll)
	entries, err := service.takeAll(context.Background(), cfg, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if changes := fake.Mutations(); len(changes) != 0 {
		t.Errorf("dry run changed: %v", changes)
	}
	summary, _ := service.Summary()
	if !summary.DryRun || summary.Tagged != 5 || summary.Unmonitored != 2 || summary.SeriesTaken != 1 || summary.FilmsQueued != 1 || summary.EpisodesWanted != 2 || summary.Following != 1 || summary.Monitored != 2 {
		t.Errorf("dry-run summary: %+v", summary)
	}
	if len(entries) != 1 {
		t.Errorf("dry run entries: %+v", entries)
	}

	fake = build()
	service, cfg = manageTestService(t, fake, config.ManageAll)
	entries, err = service.takeAll(context.Background(), cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for id, film := range fake.Films {
		if film["monitored"] != false || !slices.Equal(fake.Labels("radarr", film["tags"]), []string{model.TagFound}) {
			t.Errorf("film %d: %v", id, film)
		}
	}
	if len(entries) != 1 || entries[0].ArrID != 2 || entries[0].Source != FromLibrary || entries[0].State != Waiting {
		t.Errorf("in line: %+v", entries)
	}
	for id, series := range fake.Series {
		if series["monitorNewItems"] != "none" || series["monitored"] != true || !slices.Equal(fake.Labels("sonarr", series["tags"]), []string{model.TagFound}) {
			t.Errorf("series %d: %v", id, series)
		}
	}
	monitored := map[int]bool{}
	for id, e := range fake.Eps {
		monitored[id] = e["monitored"] == true
	}
	if want := map[int]bool{101: false, 102: true, 103: true, 104: false, 105: false, 201: false, 106: false}; !mapsEqual(monitored, want) {
		t.Errorf("episodes monitored: %v, want %v", monitored, want)
	}
	states, _ := service.sonarr.States()
	// 101 is old and was wanted; 102 aired over SearchAfter ago, and stays
	// monitored for the RSS meanwhile.
	if wanted := states["s"].Wanted; len(wanted) != 2 || wanted[101].SeriesID != 1 || wanted[102].SeriesID != 1 {
		t.Errorf("wanted: %+v", wanted)
	}

	// The next pass changes nothing more, and what was added since is new:
	// a film or a series added from outside (by hand, by a list, whatever
	// its tags) goes first.
	before := len(fake.Mutations())
	fake.Films[4] = map[string]any{"id": 4, "tmdbId": 14, "title": "Added since", "monitored": true, "hasFile": false, "added": arrfake.ISO(now.Add(time.Hour))}
	other := fake.TagID("sonarr", "from-a-list")
	for id, tags := range map[int][]int{3: nil, 4: {other}} {
		fake.Series[id] = map[string]any{"id": id, "title": "Added since", "monitored": true, "monitorNewItems": "none", "tags": tags,
			"seasons": []any{season(1, true)}, "added": arrfake.ISO(now.Add(time.Hour))}
		fake.Eps[300+id] = map[string]any{"id": 300 + id, "seriesId": id, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": arrfake.ISO(now.AddDate(0, 0, -60)), "hasFile": false, "monitored": true}
	}
	service.now = func() time.Time { return now.Add(2 * time.Hour) }
	if entries, err = service.takeAll(context.Background(), cfg, entries, false); err != nil {
		t.Fatal(err)
	}
	if got := fake.Labels("radarr", fake.Films[4]["tags"]); !slices.Equal(got, []string{model.TagFound}) || fake.Films[4]["monitored"] != false || len(entries) != 2 || entries[1].Source != FromHand {
		t.Errorf("film added since: %v, in line %+v", fake.Films[4], entries)
	}
	states, _ = service.sonarr.States()
	if wanted := states["s"].Wanted; !wanted[303].ByHand || !wanted[304].ByHand || wanted[101].ByHand {
		t.Errorf("wanted by hand: %+v", wanted)
	}
	for _, change := range fake.Mutations()[before:] {
		if strings.Contains(change, "sonarr PUT /api/v3/series/1") || strings.Contains(change, "sonarr PUT /api/v3/series/2") {
			t.Errorf("series taken again: %s", change)
		}
	}
}

func mapsEqual(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// A title tagged overtide-ignore is left alone: not tagged, unmonitored,
// taken or put in line, and its series' episodes are neither followed nor
// wanted.
func TestTakeAllLeavesIgnoredAlone(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	ignoreFilm, ignoreSeries := fake.TagID("radarr", model.TagIgnore), fake.TagID("sonarr", model.TagIgnore)
	fake.Films[1] = map[string]any{"id": 1, "tmdbId": 11, "title": "Mine", "monitored": true, "hasFile": false, "tags": []int{ignoreFilm}, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
	fake.Series[1] = map[string]any{"id": 1, "title": "Mine too", "monitored": true, "monitorNewItems": "all", "tags": []int{ignoreSeries},
		"seasons": []any{map[string]any{"seasonNumber": 1, "monitored": true}}, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
	fake.Eps[1] = map[string]any{"id": 1, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": arrfake.ISO(now.AddDate(0, 0, -60)), "hasFile": false, "monitored": true}
	fake.Eps[2] = map[string]any{"id": 2, "seriesId": 1, "seasonNumber": 1, "episodeNumber": 2, "airDateUtc": arrfake.ISO(now.Add(24 * time.Hour)), "hasFile": false, "monitored": true}
	service, cfg := manageTestService(t, fake, config.ManageAll)
	// Followed and wanted before it was left alone.
	if err := service.sonarr.SaveStates(map[string]*sonarrdriver.State{"s": {Follow: map[int]bool{1: true}, Wanted: map[int]sonarrdriver.EpisodeWant{1: {SeriesID: 1}}}}); err != nil {
		t.Fatal(err)
	}
	entries, err := service.takeAll(context.Background(), cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range fake.Mutations() {
		t.Errorf("changed: %s", change)
	}
	if len(entries) != 0 {
		t.Errorf("in line: %+v", entries)
	}
	states, _ := service.sonarr.States()
	if len(states["s"].Follow) != 0 || len(states["s"].Wanted) != 0 {
		t.Errorf("still followed or wanted: %+v", states["s"])
	}
}

// A season out with no complete pack Sonarr would take: from the same
// search, the first single release for each wanted episode it would take
// as it is, of wanted episodes only; the episodes still missing are then
// searched one by one, without waiting for a pack. Complete, the season's
// pack is an upgrade: not looked for while upgrades are off, then sent.
func TestSeasonWithoutPack(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	old := now.AddDate(0, 0, -30)
	fake.AddSeries(5, nil, arrfake.Season(1, 1, 4))
	fake.AddEpisode(1, 5, 1, old, true)
	for id := 2; id <= 4; id++ {
		fake.AddEpisode(id, 5, 1, old, false)
	}
	single := func(guid string, approved bool, episodes ...int) map[string]any {
		var mapped []map[string]any
		for _, e := range episodes {
			mapped = append(mapped, map[string]any{"id": e})
		}
		return map[string]any{"guid": guid, "indexerId": 1, "title": guid, "fullSeason": false, "seasonNumber": 1, "downloadAllowed": true, "approved": approved, "mappedEpisodeInfo": mapped}
	}
	fake.Releases[1] = []map[string]any{
		{"guid": "pack", "indexerId": 1, "title": "pack", "fullSeason": true, "seasonNumber": 1, "rejections": []string{"Language is not wanted in profile"}, "downloadAllowed": true},
		single("e2", true, 2), single("e2-again", true, 2), single("e3", false, 3), single("e1e4", true, 1, 4),
	}
	service, cfg := manageTestService(t, fake, config.ManageAll)
	wanted := map[int]sonarrdriver.EpisodeWant{2: {SeriesID: 5, Season: 1}, 3: {SeriesID: 5, Season: 1}, 4: {SeriesID: 5, Season: 1}}
	if err := service.sonarr.SaveStates(map[string]*sonarrdriver.State{"s": {Follow: map[int]bool{}, Wanted: wanted}}); err != nil {
		t.Fatal(err)
	}

	// The run's searches left: one after the pack's, episode 3 then.
	if used, err := service.replaceSeasons(context.Background(), cfg, 2, false, false); err != nil || used != 2 {
		t.Fatalf("%d searches, %v", used, err)
	}
	if got := fake.Searches(); !slices.Equal(got, []string{"grab e2", "search episode 3"}) {
		t.Fatalf("grabbed: %v", got)
	}
	states, _ := service.sonarr.States()
	pack := states["s"].Seasons["5/1"]
	if pack == nil || pack.Tries != 1 || !pack.GrabbedAt.IsZero() || !states["s"].Wanted[2].Grabbed {
		t.Fatalf("after the search: %+v, %+v", pack, states["s"].Wanted)
	}
	if started, _ := service.started(); len(started) != 0 {
		t.Errorf("single episodes counted in the titles a day: %d", len(started))
	}
	// The next run: episode 4 first, no pack search before its time, and
	// nothing searched twice.
	if used, err := service.replaceSeasons(context.Background(), cfg, 3, false, false); err != nil || used != 1 {
		t.Fatalf("next run: %d searches, %v", used, err)
	}
	if _, err := service.sonarr.SearchEpisodes(context.Background(), cfg, 3, false, false); err != nil {
		t.Fatal(err)
	}
	if got := fake.Searches(); !slices.Equal(got, []string{"grab e2", "search episode 3", "search episode 4"}) {
		t.Fatalf("searched: %v", got)
	}

	// Every episode came: the pack, now there, waits for upgrades.
	for id := 2; id <= 4; id++ {
		fake.Eps[id]["hasFile"] = true
	}
	states, _ = service.sonarr.States()
	states["s"].Wanted = map[int]sonarrdriver.EpisodeWant{}
	if err := service.sonarr.SaveStates(states); err != nil {
		t.Fatal(err)
	}
	fake.Releases[1] = []map[string]any{{"guid": "pack-fr", "indexerId": 1, "title": "pack-fr", "fullSeason": true, "seasonNumber": 1, "rejections": []string{"Existing file on disk is of equal or higher preference: WEBDL-1080p"}, "downloadAllowed": true}}
	service.now = func() time.Time { return now.Add(3 * 24 * time.Hour) }
	if used, err := service.replaceSeasons(context.Background(), cfg, 3, false, false); err != nil || used != 0 {
		t.Fatalf("complete, as a missing season: %d, %v", used, err)
	}
	if used, err := service.searchUpgrades(context.Background(), cfg, nil, 3, false); err != nil || used != 0 {
		t.Fatalf("upgrades off: %d, %v", used, err)
	}
	cfg.Adding.UpgradesPerDay = 1
	if used, err := service.searchUpgrades(context.Background(), cfg, nil, 3, false); err != nil || used != 1 {
		t.Fatalf("as an upgrade: %d, %v", used, err)
	}
	if got := fake.Searches(); got[len(got)-1] != "grab pack-fr" {
		t.Fatalf("grabbed: %v", got)
	}
	states, _ = service.sonarr.States()
	if pack := states["s"].Seasons["5/1"]; pack.Release != "pack-fr" || pack.GrabbedAt.IsZero() {
		t.Fatalf("the pack sent: %+v", pack)
	}
	if used, _ := service.searchUpgrades(context.Background(), cfg, nil, 3, false); used != 0 {
		t.Errorf("past the upgrades a day: %d", used)
	}
}

// A film just taken from Radarr stays on the list though the inventory
// doesn't have it yet (it was unmonitored, and is never taken again); one
// gone for longer than a refresh takes has left Radarr.
func TestJustTakenSurvivesAnOldInventory(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	service, _ := manageTestService(t, arrfake.New(now), config.ManageAll)
	entries := []Entry{
		{Key: movieKey(1), Kind: model.Movie, ServiceID: "r", Source: FromHand, State: Waiting, ArrID: 1, AddedAt: now},
		{Key: movieKey(2), Kind: model.Movie, ServiceID: "r", Source: FromHand, State: Waiting, ArrID: 2, AddedAt: now.Add(-justTaken - time.Minute)},
	}
	got := service.match(entries)
	if len(got) != 1 || got[0].ArrID != 1 {
		t.Errorf("kept: %+v", got)
	}
}

// What "Manage all" took goes back exactly as it was: a title the
// operator tags overtide-ignore at once (keeping overtide-keep), and every
// one when Overtide is set back to manage only what it added: monitoring
// as recorded before it was taken, without overtide-found, out of the
// lists.
func TestGiveBack(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	season := func(n int, monitored bool) map[string]any {
		return map[string]any{"seasonNumber": n, "monitored": monitored}
	}
	fake.Films[1] = map[string]any{"id": 1, "tmdbId": 11, "title": "Ignored later", "monitored": true, "hasFile": false, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
	fake.Films[2] = map[string]any{"id": 2, "tmdbId": 12, "title": "Wanted", "monitored": true, "hasFile": false, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
	fake.Films[3] = map[string]any{"id": 3, "tmdbId": 13, "title": "Removed", "monitored": false, "hasFile": false, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
	fake.Series[1] = map[string]any{"id": 1, "title": "Airing", "monitored": true, "monitorNewItems": "all", "seasons": []any{season(1, true), season(2, false)}, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
	ep := func(id, season int, aired time.Time, monitored bool) {
		fake.Eps[id] = map[string]any{"id": id, "seriesId": 1, "seasonNumber": season, "episodeNumber": id, "airDateUtc": arrfake.ISO(aired), "hasFile": false, "monitored": monitored}
	}
	ep(101, 1, now.AddDate(0, 0, -60), true)
	ep(102, 1, now.AddDate(0, 0, -3), true)
	ep(103, 2, now.AddDate(0, 0, -40), false)
	service, cfg := manageTestService(t, fake, config.ManageAll)
	ctx := context.Background()
	entries, err := service.takeAll(ctx, cfg, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if fake.Films[2]["monitored"] != false || fake.Series[1]["monitorNewItems"] != "none" || len(entries) != 2 {
		t.Fatalf("not taken: %v %v %+v", fake.Films[2], fake.Series[1], entries)
	}

	// Ignored by the operator, kept too: given back at once.
	fake.Films[1]["tags"] = append(fake.Films[1]["tags"].([]int), fake.TagID("radarr", model.TagIgnore), fake.TagID("radarr", model.TagKeep))
	if entries, err = service.giveBack(ctx, cfg, entries, false); err != nil {
		t.Fatal(err)
	}
	got := fake.Labels("radarr", fake.Films[1]["tags"])
	slices.Sort(got)
	if fake.Films[1]["monitored"] != true || !slices.Equal(got, []string{model.TagIgnore, model.TagKeep}) || len(entries) != 1 {
		t.Fatalf("ignored: %v %v, in line %+v", fake.Films[1], got, entries)
	}

	// Back to what Overtide adds: all of it as it was.
	cfg.Adding.Manage = config.ManageOwn
	if entries, err = service.giveBack(ctx, cfg, entries, false); err != nil {
		t.Fatal(err)
	}
	if fake.Films[2]["monitored"] != true || fake.Films[3]["monitored"] != false || len(fake.Labels("radarr", fake.Films[2]["tags"])) != 0 {
		t.Errorf("films: %v %v", fake.Films[2], fake.Films[3])
	}
	s := fake.Series[1]
	seasons := map[string]bool{}
	for _, raw := range s["seasons"].([]any) {
		m := raw.(map[string]any)
		seasons[fmt.Sprint(m["seasonNumber"])] = m["monitored"] == true
	}
	if s["monitorNewItems"] != "all" || s["monitored"] != true || !seasons["1"] || seasons["2"] || len(fake.Labels("sonarr", s["tags"])) != 0 {
		t.Errorf("series: %v", s)
	}
	for id, want := range map[int]bool{101: true, 102: true, 103: false} {
		if (fake.Eps[id]["monitored"] == true) != want {
			t.Errorf("episode %d monitored: %v", id, fake.Eps[id]["monitored"])
		}
	}
	if len(entries) != 0 {
		t.Errorf("still in line: %+v", entries)
	}
	if states, _ := service.sonarr.States(); len(states["s"].Wanted) != 0 || states["s"].Follow[1] {
		t.Errorf("still followed: %+v", states["s"])
	}
	if taken, _ := service.sonarr.Taken(); len(taken) != 0 {
		t.Errorf("record left: %v", taken)
	}
}
