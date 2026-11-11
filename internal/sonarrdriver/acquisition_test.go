package sonarrdriver

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/tries"
)

// With "Only what it adds", a season Overtide holds and that's airing is
// followed, and what it doesn't manage, holds no file of, or was told to
// ignore is left alone.
func TestOwnModeFollowsAiringSeasons(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	added := fake.TagID("sonarr", model.TagAdded)
	ignored := fake.TagID("sonarr", model.TagIgnore)
	fake.AddSeries(1, []int{added}, arrfake.Season(1, 2, 8))
	fake.AddEpisode(101, 1, 1, now.AddDate(0, 0, -3), false)
	fake.AddEpisode(102, 1, 1, now.Add(24*time.Hour), false)
	fake.AddSeries(2, nil, arrfake.Season(1, 2, 8)) // not Overtide's
	fake.AddEpisode(201, 2, 1, now.AddDate(0, 0, -3), false)
	fake.AddSeries(3, []int{added}, arrfake.Season(1, 0, 8)) // no file: nobody wants it
	fake.AddEpisode(301, 3, 1, now.AddDate(0, 0, -3), false)
	fake.AddSeries(4, []int{added, ignored}, arrfake.Season(1, 2, 8))
	fake.AddEpisode(401, 4, 1, now.AddDate(0, 0, -3), false)
	driver, cfg := testDriver(t, fake, config.ManageOwn)

	driver.Tend(context.Background(), cfg, nil, false)
	if fake.Eps[101]["monitored"] != true || fake.Eps[102]["monitored"] != true {
		t.Errorf("airing episodes not monitored: %v, %v", fake.Eps[101], fake.Eps[102])
	}
	for _, id := range []int{201, 301, 401} {
		if fake.Eps[id]["monitored"] == true {
			t.Errorf("episode %d monitored", id)
		}
	}
	states, _ := driver.States()
	if wanted := states["s"].Wanted; len(wanted) != 1 || wanted[101].AiredAt.IsZero() {
		t.Fatalf("wanted: %+v", wanted)
	}

	// Searched at once, then each day until tries.ReleaseGrace, in that order
	// before anything else.
	used, err := driver.SearchEpisodes(context.Background(), cfg, 3, false, true)
	if err != nil || used != 1 || !slices.Equal(fake.Searches(), []string{"search episode 101"}) {
		t.Fatalf("%d searches, %v: %v", used, err, fake.Searches())
	}
	states, _ = driver.States()
	if want := states["s"].Wanted[101]; want.Tries != 1 || !want.NextTry.Equal(now.Add(24*time.Hour)) {
		t.Errorf("next try: %+v", want)
	}
	if next := driver.wantRetry(EpisodeWant{AiredAt: now.AddDate(0, 0, -5), Tries: 5}); !next.Equal(now.Add(24 * time.Hour)) {
		t.Errorf("within the grace: %v", next)
	}
	want := EpisodeWant{AiredAt: now.Add(-tries.ReleaseGrace), Tries: 5}
	if next := driver.wantRetry(want); !next.Equal(now.Add(tries.RetryDelay(5))) {
		t.Errorf("past the grace: %v", next)
	}
	// The RSS gets it: no more searches for it.
	fake.Queued[101] = true
	driver.now = func() time.Time { return now.Add(25 * time.Hour) }
	if used, _ := driver.SearchEpisodes(context.Background(), cfg, 3, false, true); used != 0 {
		t.Errorf("searched a queued episode")
	}
	states, _ = driver.States()
	if !states["s"].Wanted[101].Grabbed {
		t.Errorf("not followed: %+v", states["s"].Wanted[101])
	}
}

// A season with files that lacks episodes that aired is put in the wanted
// episodes, unless it lost a file to something other than an upgrade.
func TestFindIncomplete(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	old := now.AddDate(-1, 0, 0)
	fake.AddSeries(1, nil, arrfake.Season(1, 1, 3), arrfake.Season(2, 2, 2), arrfake.Season(3, 0, 2), arrfake.Season(4, 1, 2), arrfake.Season(5, 1, 2), arrfake.Season(0, 1, 2))
	fake.AddEpisode(11, 1, 1, old, true)
	fake.AddEpisode(12, 1, 1, old, false)                 // lacking
	fake.AddEpisode(13, 1, 1, now.Add(-time.Hour), false) // just aired: the RSS's turn
	fake.AddEpisode(21, 1, 2, old, true)                  // complete
	fake.AddEpisode(31, 1, 3, old, false)                 // no file in the season
	fake.AddEpisode(41, 1, 4, old, true)                  //
	fake.AddEpisode(42, 1, 4, old, false)                 // removed
	fake.AddEpisode(51, 1, 5, old, true)                  //
	fake.AddEpisode(52, 1, 5, old, false)                 // only upgraded
	fake.AddEpisode(1, 1, 0, old, true)                   // a special
	fake.AddEpisode(2, 1, 0, old, false)                  // specials aren't seasons
	fake.History["1"] = []map[string]any{
		{"eventType": "episodeFileDeleted", "episodeId": 42, "date": arrfake.ISO(now.AddDate(0, -1, 0)), "data": map[string]any{"reason": "Manual"}},
		{"eventType": "episodeFileDeleted", "episodeId": 52, "date": arrfake.ISO(now.AddDate(0, -1, 0)), "data": map[string]any{"reason": "Upgrade"}},
		{"eventType": "episodeFileDeleted", "episodeId": 12, "date": arrfake.ISO(now.AddDate(-2, 0, 0)), "data": map[string]any{"reason": "Manual"}}, // past the cooldown
	}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	driver.tendOwnAll(t, cfg)
	states, _ := driver.States()
	var ids []int
	for id := range states["s"].Wanted {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int{12, 52}) {
		t.Errorf("wanted: %v", ids)
	}
	// Not looked for again before LookEvery.
	fake.AddEpisode(14, 1, 1, old, false)
	driver.tendOwnAll(t, cfg)
	states, _ = driver.States()
	if _, there := states["s"].Wanted[14]; there {
		t.Error("looked again too soon")
	}
}

// tendOwnAll tends every series, as Take does for "Manage all" (the series
// here were there before it, so all get TagFound).
func (driver *Driver) tendOwnAll(t *testing.T, cfg config.Config) {
	t.Helper()
	if _, err := driver.Take(context.Background(), cfg, nil, func(time.Time) (string, bool) { return model.TagFound, false }, false); err != nil {
		t.Fatal(err)
	}
}

// Only an episode just aired goes first: an old hole in a season still
// airing takes its turn after wishes.
func TestOldHoleInAnAiringSeasonIsntFirst(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	fake.AddEpisode(1, 1, 1, now.AddDate(0, 0, -3), false)
	fake.AddEpisode(2, 1, 1, now.Add(-tries.ReleaseGrace-time.Hour), false)
	fake.AddEpisode(3, 1, 1, now.AddDate(0, 0, 4), false)
	driver, cfg := testDriver(t, fake, config.ManageAll)
	// Saved by an older version: no air date.
	wanted := map[int]EpisodeWant{1: {SeriesID: 1, Season: 1}, 2: {SeriesID: 1, Season: 1}}
	if err := driver.SaveStates(map[string]*State{"s": {Wanted: wanted}}); err != nil {
		t.Fatal(err)
	}
	if used, err := driver.SearchEpisodes(context.Background(), cfg, 5, false, true); err != nil || used != 1 {
		t.Fatalf("%d searches, %v", used, err)
	}
	if got := fake.Searches(); !slices.Equal(got, []string{"search episode 1"}) {
		t.Errorf("first: %v", got)
	}
	if used, err := driver.SearchEpisodes(context.Background(), cfg, 5, false, false); err != nil || used != 1 {
		t.Fatalf("%d searches, %v", used, err)
	}
	if got := fake.Searches(); !slices.Equal(got, []string{"search episode 1", "search episode 2"}) {
		t.Errorf("then: %v", got)
	}
	states, _ := driver.States()
	want := states["s"].Wanted[2]
	if want.Episode != 2 || want.AiredAt.IsZero() || want.Result != "Nothing found (try 1)" || want.LastTry.IsZero() {
		t.Errorf("filled in: %+v", want)
	}
}

// The page's lists: episodes wanted and seasons, as last saved, the next to
// be searched first.
func TestWantedRows(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	fake.AddSeries(1, nil, arrfake.Season(1, 1, 3), arrfake.Season(2, 1, 3))
	driver, _ := testDriver(t, fake, config.ManageAll)
	if err := driver.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := &State{
		Wanted: map[int]EpisodeWant{
			1: {SeriesID: 1, Season: 1, Episode: 4, AiredAt: now.AddDate(0, 0, -3), NextTry: now.Add(time.Hour), Result: "Nothing found (try 1)"},
			2: {SeriesID: 1, Season: 1, Episode: 2, AiredAt: now.AddDate(-1, 0, 0)},
			3: {SeriesID: 1, Season: 2, Episode: 1, AiredAt: now.AddDate(-1, 0, 0), Grabbed: true, Result: "Downloading"},
			4: {SeriesID: 1, Season: 3, Episode: 1, AiredAt: now.AddDate(-1, 0, 0)},
		},
		Seasons: map[string]*SeasonPack{
			seasonKey(1, 3): {SeriesID: 1, Season: 3},
			seasonKey(1, 4): {SeriesID: 1, Season: 4, Done: true},
			seasonKey(1, 5): {SeriesID: 1, Season: 5, Tries: tries.Max},
			seasonKey(1, 6): {SeriesID: 1, Season: 6, GrabbedAt: now, Release: "Pack"},
		},
	}
	if err := driver.SaveStates(map[string]*State{"s": state}); err != nil {
		t.Fatal(err)
	}
	episodes, seasons, err := driver.Wanted()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]WantedRow{}
	for _, r := range append(episodes, seasons...) {
		got[r.Title] = r
	}
	if r := got["Series 1 S1E4"]; r.Why != "Just aired" || r.Result != "Nothing found (try 1)" || !r.When.Equal(now.Add(time.Hour)) {
		t.Errorf("just aired: %+v", r)
	}
	if r := got["Series 1 S1E2"]; r.Why != "Missing" || r.Result != "Not searched yet" {
		t.Errorf("missing: %+v", r)
	}
	if r := got["Series 1 S2E1"]; r.Result != "Downloading" {
		t.Errorf("grabbed: %+v", r)
	}
	if r := got["Series 1 S3E1"]; r.Result != "Waits for its whole season" {
		t.Errorf("its season comes whole: %+v", r)
	}
	if _, there := got["Series 1 S4"]; there || len(seasons) != 3 {
		t.Errorf("seasons: %+v", seasons)
	}
	if r := got["Series 1 S5"]; !strings.Contains(r.Result, "one by one") {
		t.Errorf("no pack: %+v", r)
	}
	if r := got["Series 1 S6"]; r.Result != "Downloading: Pack" {
		t.Errorf("pack: %+v", r)
	}
	if episodes[len(episodes)-1].Title != "Series 1 S1E4" {
		t.Errorf("order: %+v", episodes)
	}
}

// A season still short after a pack grabbed past its last episode isn't
// searched again, and says so; a pack from before that, or one nothing of
// was imported, proves nothing; once complete, it's back to normal.
func TestSeasonShortAfterAPack(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	old := now.AddDate(-1, 0, 0)
	fake.AddSeries(1, nil, arrfake.Season(1, 1, 3), arrfake.Season(2, 1, 2), arrfake.Season(3, 1, 2), arrfake.Season(4, 1, 2))
	fake.AddEpisode(41, 1, 4, old, true)
	fake.AddEpisode(42, 1, 4, old, false)
	fake.AddEpisode(11, 1, 1, old, true)
	fake.AddEpisode(12, 1, 1, old, false)
	fake.AddEpisode(13, 1, 1, old, false)
	fake.AddEpisode(21, 1, 2, old, true)
	fake.AddEpisode(22, 1, 2, old, false)
	fake.AddEpisode(31, 1, 3, old, true)
	fake.AddEpisode(32, 1, 3, old, false)
	pack := map[string]any{"releaseType": "SeasonPack"}
	fake.History["1"] = []map[string]any{
		// Season 1: the pack came after the season ended.
		{"eventType": "grabbed", "episodeId": 11, "downloadId": "A", "date": arrfake.ISO(now.AddDate(0, -1, 0)), "data": pack},
		{"eventType": "downloadFolderImported", "episodeId": 11, "downloadId": "A", "date": arrfake.ISO(now.AddDate(0, -1, 0)), "data": map[string]any{}},
		// Season 4: a pack nothing of was imported.
		{"eventType": "grabbed", "episodeId": 41, "downloadId": "D", "date": arrfake.ISO(now.AddDate(0, -1, 0)), "data": pack},
		// Season 2: only single episodes.
		{"eventType": "grabbed", "episodeId": 21, "downloadId": "B", "date": arrfake.ISO(now.AddDate(0, -1, 0)), "data": map[string]any{"releaseType": "SingleEpisode"}},
		{"eventType": "downloadFolderImported", "episodeId": 21, "downloadId": "B", "date": arrfake.ISO(now.AddDate(0, -1, 0)), "data": map[string]any{}},
		// Season 3: a pack from before its last episode aired.
		{"eventType": "grabbed", "episodeId": 31, "downloadId": "C", "date": arrfake.ISO(old.AddDate(0, -1, 0)), "data": pack},
		{"eventType": "downloadFolderImported", "episodeId": 31, "downloadId": "C", "date": arrfake.ISO(old.AddDate(0, -1, 0)), "data": map[string]any{}},
	}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	if err := driver.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Season 1 was wanted already, as older versions left it.
	if err := driver.SaveStates(map[string]*State{"s": {Follow: map[int]bool{}, Wanted: map[int]EpisodeWant{12: {SeriesID: 1, Season: 1}}}}); err != nil {
		t.Fatal(err)
	}
	driver.tendOwnAll(t, cfg)
	states, _ := driver.States()
	state := states["s"]
	var ids []int
	for id := range state.Wanted {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int{22, 32, 42}) {
		t.Errorf("wanted: %v", ids)
	}
	if p := state.Seasons[seasonKey(1, 1)]; p == nil || p.Short != 2 || p.pending() {
		t.Fatalf("season 1: %+v", p)
	}
	// No search for it, whole or one by one.
	fake.Releases[1] = []map[string]any{{"guid": "again", "indexerId": 1, "title": "again", "fullSeason": true, "seasonNumber": 1, "downloadAllowed": true}}
	if _, _, err := driver.ReplaceSeasons(context.Background(), cfg, 5, false, false); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fake.Searches(), "grab again") {
		t.Error("searched again for the season")
	}
	_, seasons, err := driver.Wanted()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range seasons {
		if r.Title == "Series 1 S1" {
			found = r.Why == "Needs a look" && strings.Contains(r.Result, "2 episodes are still missing")
		}
	}
	if !found {
		t.Errorf("rows: %+v", seasons)
	}
	// Complete since: back to normal.
	fake.Mu.Lock()
	fake.Series[1]["seasons"] = []any{arrfake.Season(1, 3, 3), arrfake.Season(2, 1, 2), arrfake.Season(3, 1, 2), arrfake.Season(4, 1, 2)}
	fake.Eps[12]["hasFile"], fake.Eps[13]["hasFile"] = true, true
	fake.Mu.Unlock()
	driver.now = func() time.Time { return now.Add(LookEvery + time.Hour) }
	driver.tendOwnAll(t, cfg)
	states, _ = driver.States()
	if states["s"].Seasons[seasonKey(1, 1)].short() {
		t.Error("still marked short")
	}
}

// A finished download goes off the queue, not off the client, once every
// episode of it has its file; nothing else does.
func TestClearFinished(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	fake.AddSeries(1, nil, arrfake.Season(1, 2, 2))
	fake.AddSeries(2, []int{fake.TagID("sonarr", model.TagIgnore)}, arrfake.Season(1, 1, 1))
	fake.AddEpisode(1, 1, 1, now.AddDate(-1, 0, 0), true)
	fake.AddEpisode(2, 1, 1, now.AddDate(-1, 0, 0), true)
	fake.AddEpisode(3, 1, 1, now.AddDate(-1, 0, 0), false)
	fake.AddEpisode(4, 2, 1, now.AddDate(-1, 0, 0), true)
	item := func(id int, download string, series, episode int, status, tracked, state string) map[string]any {
		return map[string]any{"id": id, "downloadId": download, "seriesId": series, "episodeId": episode, "status": status, "trackedDownloadStatus": tracked, "trackedDownloadState": state,
			"episodeHasFile": false} // what the queue says isn't what's true
	}
	fake.FullQueue = []map[string]any{
		item(1, "done", 1, 1, "completed", "warning", "importBlocked"),
		item(2, "done", 1, 2, "completed", "warning", "importPending"),
		item(3, "lacking", 1, 1, "completed", "warning", "importBlocked"),
		item(4, "lacking", 1, 3, "completed", "warning", "importBlocked"), // an episode without its file
		item(5, "running", 1, 1, "downloading", "warning", "importPending"),
		item(6, "ignored", 2, 4, "completed", "warning", "importBlocked"),
		item(7, "followed", 1, 1, "completed", "warning", "importBlocked"), // its pack's being imported
		item(8, "importing", 1, 1, "completed", "warning", "importing"),
		item(9, "upgrade", 1, 1, "completed", "ok", "importPending"), // not imported yet
	}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	if err := driver.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	states := map[string]*State{"s": {Seasons: map[string]*SeasonPack{"1/1": {SeriesID: 1, Season: 1, GrabbedAt: now, DownloadID: "followed"}}}}
	if err := driver.SaveStates(states); err != nil {
		t.Fatal(err)
	}
	driver.ClearFinished(context.Background(), cfg, true)
	if len(fake.Mutations()) != 0 {
		t.Fatalf("dry run: %v", fake.Mutations())
	}
	driver.ClearFinished(context.Background(), cfg, false)
	if !slices.Equal(fake.Mutations(), []string{"sonarr DELETE /api/v3/queue/bulk", "forget [1 2] removeFromClient=false"}) {
		t.Errorf("changes: %v", fake.Mutations())
	}
}

// A pack with a file matching no episode stays and needs a look, even with
// every episode there; one with files thought no upgrade replaces the older
// ones first, and goes once that's done.
func TestRefusedFiles(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	old := now.AddDate(-1, 0, 0)
	fake.AddSeries(1, nil, arrfake.Season(1, 2, 2), arrfake.Season(2, 2, 2))
	file := func(id, seasonNumber int, added time.Time) {
		fake.Files[id] = map[string]any{"id": id, "seasonNumber": seasonNumber, "dateAdded": arrfake.ISO(added)}
	}
	ep := func(id, seasonNumber, fileID int) {
		fake.AddEpisode(id, 1, seasonNumber, old, true)
		fake.Eps[id]["episodeFileId"] = fileID
	}
	ep(1, 1, 101)
	ep(2, 1, 102)
	ep(3, 2, 103)
	ep(4, 2, 104)
	file(101, 1, now.Add(-time.Hour))
	file(102, 1, now.Add(-time.Hour))
	file(103, 2, now.Add(-time.Hour)) // the pack's
	file(104, 2, old)                 // older: it thought the pack's no upgrade
	added := arrfake.ISO(now.Add(-2 * time.Hour))
	item := func(id int, download string, seasonNumber, episode int, title, message string) map[string]any {
		return map[string]any{"id": id, "downloadId": download, "seriesId": 1, "seasonNumber": seasonNumber, "episodeId": episode, "added": added, "status": "completed", "trackedDownloadStatus": "warning", "trackedDownloadState": "importPending",
			"statusMessages": []map[string]any{{"title": title, "messages": []string{message}}}}
	}
	fake.FullQueue = []map[string]any{
		item(1, "extra", 1, 1, "Extra.S01E27.mkv", "Invalid season or episode"),
		item(2, "extra", 1, 2, "Show.S01E02.mkv", "Episode file already imported at 11/02/2026 00:08:27"),
		item(3, "worse", 2, 3, "Show.S02E01.mkv", "Episode file already imported at 11/02/2026 00:08:27"),
		item(4, "worse", 2, 4, "Show.S02E02.mkv", "Not an upgrade for existing episode file(s). Existing quality: WEBDL-2160p. New Quality WEBDL-1080p."),
	}
	two := 2
	fake.ImportAt = []map[string]any{
		{"path": "/d/Show.S02E01.mkv", "seasonNumber": two, "episodes": []map[string]any{{"id": 3}}, "quality": map[string]any{}, "languages": []any{}, "downloadId": "worse"},
		{"path": "/d/Show.S02E02.mkv", "seasonNumber": two, "episodes": []map[string]any{{"id": 4}}, "quality": map[string]any{}, "languages": []any{}, "downloadId": "worse"},
		// Another season's, and the series' own file: never imported.
		{"path": "/d/Show.S01E01.mkv", "seasonNumber": 1, "episodes": []map[string]any{{"id": 1}}, "quality": map[string]any{}, "languages": []any{}, "downloadId": "worse"},
		{"path": "/data/Series/1/Show - S02E02.mkv", "seasonNumber": two, "episodes": []map[string]any{{"id": 4}}, "quality": map[string]any{}, "languages": []any{}},
	}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	if err := driver.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := driver.SaveStates(map[string]*State{"s": {Wanted: map[int]EpisodeWant{1: {SeriesID: 1, Season: 1}}}}); err != nil {
		t.Fatal(err)
	}
	driver.ClearFinished(context.Background(), cfg, false)
	if len(fake.Imported) != 1 || !slices.Equal(fake.Imported[0], []string{"/d/Show.S02E02.mkv"}) {
		t.Errorf("imported: %v", fake.Imported)
	}
	if slices.ContainsFunc(fake.Mutations(), func(c string) bool { return strings.HasPrefix(c, "forget") }) {
		t.Errorf("forgot too soon: %v", fake.Mutations())
	}
	states, _ := driver.States()
	pack := states["s"].Seasons[seasonKey(1, 1)]
	if pack == nil || !slices.Equal(pack.Unmatched, []string{"Extra.S01E27.mkv"}) || pack.pending() {
		t.Fatalf("unmatched: %+v", pack)
	}
	if len(states["s"].Wanted) != 0 {
		t.Errorf("still wanted: %+v", states["s"].Wanted)
	}
	_, seasons, _ := driver.Wanted()
	if len(seasons) != 1 || seasons[0].Why != "Needs a look" || !strings.Contains(seasons[0].Result, "Extra.S01E27.mkv") {
		t.Errorf("rows: %+v", seasons)
	}
	// Imported: nothing older left. It goes, the other stays.
	fake.Mu.Lock()
	fake.Files[104]["dateAdded"] = arrfake.ISO(now)
	fake.Mu.Unlock()
	driver.ClearFinished(context.Background(), cfg, false)
	if !slices.Contains(fake.Mutations(), "forget [3 4] removeFromClient=false") || len(fake.Imported) != 1 {
		t.Errorf("changes: %v, imported %v", fake.Mutations(), fake.Imported)
	}
	// The extra file's gone from the queue: back to normal.
	fake.Mu.Lock()
	fake.FullQueue = []map[string]any{}
	fake.Mu.Unlock()
	driver.ClearFinished(context.Background(), cfg, false)
	states, _ = driver.States()
	if states["s"].Seasons[seasonKey(1, 1)].short() {
		t.Error("still needs a look")
	}
}
