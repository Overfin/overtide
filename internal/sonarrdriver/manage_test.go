package sonarrdriver

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
	"github.com/overfin/overtide/internal/tries"
)

// newTestDriver is a driver at now, with the series Overtide manages as
// the adding side has them: its tags, or all with "Manage all".
func newTestDriver(t *testing.T, cfg config.Config, now time.Time) *Driver {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	inv := inventory.New(cfg, database)
	driver := New(inv, func() time.Time { return now })
	driver.Managed = func(m model.Media) bool {
		return inv.Config().Adding.ManagesAll() || slices.ContainsFunc(m.Tags, model.IsManagedTag)
	}
	return driver
}

// testDriver is a driver on the fake, at its time.
func testDriver(t *testing.T, fake *arrfake.Arrs, manage string) (*Driver, config.Config) {
	t.Helper()
	cfg := arrfake.Config(t, fake, manage)
	driver := newTestDriver(t, cfg, fake.Now)
	tries.PollEvery = time.Millisecond
	return driver, cfg
}

// Wanted episodes are searched one at a time within the run's budget: a
// grab is followed until imported, then leaves the list; nothing found is
// tried again later.
func TestSearchEpisodes(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	for _, id := range []int{1, 2, 3} {
		fake.Eps[id] = map[string]any{"id": id, "seriesId": 9, "seasonNumber": 1, "episodeNumber": id, "airDateUtc": arrfake.ISO(now.AddDate(0, -1, 0)), "hasFile": false, "monitored": false}
	}
	fake.GrabOn[1] = true
	driver, cfg := testDriver(t, fake, config.ManageAll)
	states := map[string]*State{"s": {Follow: map[int]bool{}, Wanted: map[int]EpisodeWant{1: {SeriesID: 9}, 2: {SeriesID: 9}, 3: {SeriesID: 9, NextTry: now.Add(time.Hour)}}}}
	if err := driver.SaveStates(states); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.SearchEpisodes(context.Background(), cfg, 2, false, false); err != nil {
		t.Fatal(err)
	}
	var searched []string
	for _, change := range fake.Mutations() {
		if strings.HasPrefix(change, "search") {
			searched = append(searched, change)
		}
	}
	slices.Sort(searched)
	if !slices.Equal(searched, []string{"search episode 1", "search episode 2"}) {
		t.Errorf("searched: %v", searched)
	}
	states, _ = driver.States()
	wanted := states["s"].Wanted
	if !wanted[1].Grabbed || wanted[2].Grabbed || wanted[2].Tries != 1 || !wanted[2].NextTry.After(now) {
		t.Errorf("after the searches: %+v", wanted)
	}
	// Imported: it leaves the list.
	fake.Mu.Lock()
	delete(fake.Queued, 1)
	fake.Eps[1]["hasFile"] = true
	fake.Mu.Unlock()
	if _, err := driver.SearchEpisodes(context.Background(), cfg, 0, false, false); err != nil {
		t.Fatal(err)
	}
	states, _ = driver.States()
	if _, there := states["s"].Wanted[1]; there {
		t.Errorf("imported episode still wanted: %+v", states["s"].Wanted)
	}
}

// The episodes of a series added by hand are SearchByHand's alone, airing
// or not: the other searches leave them.
func TestSearchByHand(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	for _, id := range []int{1, 2} {
		fake.Eps[id] = map[string]any{"id": id, "seriesId": 9, "seasonNumber": 1, "episodeNumber": id, "airDateUtc": arrfake.ISO(now.AddDate(0, -1, 0)), "hasFile": false, "monitored": false}
	}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	if err := driver.SaveStates(map[string]*State{"s": {Follow: map[int]bool{}, Wanted: map[int]EpisodeWant{1: {SeriesID: 9, ByHand: true}, 2: {SeriesID: 9}}}}); err != nil {
		t.Fatal(err)
	}
	searched := func() []string {
		var out []string
		for _, change := range fake.Mutations() {
			if strings.HasPrefix(change, "search") {
				out = append(out, change)
			}
		}
		return out
	}
	if _, err := driver.SearchByHand(context.Background(), cfg, 5, false); err != nil {
		t.Fatal(err)
	}
	if got := searched(); !slices.Equal(got, []string{"search episode 1"}) {
		t.Errorf("by hand: %v", got)
	}
	if _, err := driver.SearchEpisodes(context.Background(), cfg, 5, false, false); err != nil {
		t.Fatal(err)
	}
	if got := searched(); !slices.Equal(got, []string{"search episode 1", "search episode 2"}) {
		t.Errorf("then the others: %v", got)
	}
}

// A season whose episodes all aired SeasonWait ago is replaced by a whole
// pack: the first complete one Sonarr refused only for the files already
// there. Its single episodes wait meanwhile. Once downloaded, the pack's
// files Sonarr didn't take are imported in place of the older ones.
func TestReplaceSeasons(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	ep := func(id, season int, aired time.Time, fileID int) {
		fake.Eps[id] = map[string]any{"id": id, "seriesId": 5, "seasonNumber": season, "episodeNumber": id, "airDateUtc": arrfake.ISO(aired), "hasFile": fileID != 0, "episodeFileId": fileID, "monitored": false}
		if fileID != 0 {
			fake.Files[fileID] = map[string]any{"id": fileID, "seasonNumber": season, "dateAdded": arrfake.ISO(now.AddDate(-1, 0, 0))}
		}
	}
	// Season 1 finished over FreshSeason ago, so it's a season to fill, not
	// last episodes to fetch alone.
	ep(1, 1, now.AddDate(0, 0, -40), 11)
	ep(2, 1, now.AddDate(0, 0, -40), 0)
	ep(3, 1, now.AddDate(0, 0, -39), 13)
	ep(4, 2, now.AddDate(0, 0, -3), 0) // its season isn't out long enough
	full := func(guid string, rejections ...string) map[string]any {
		return map[string]any{"guid": guid, "indexerId": 1, "title": guid, "fullSeason": true, "seasonNumber": 1, "rejections": rejections, "downloadAllowed": true}
	}
	fake.Releases[1] = []map[string]any{
		{"guid": "single", "indexerId": 1, "title": "single", "fullSeason": false, "seasonNumber": 1, "downloadAllowed": true},
		full("unwanted", "Quality is not wanted in profile"),
		full("pack", "Existing file on disk is of equal or higher preference: WEBDL-1080p"),
		full("later"),
	}
	// Its profile: the files' quality and the packs' are the same one.
	fake.ProfileItems = []any{map[string]any{"quality": map[string]any{"id": 0}}}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	wanted := map[int]EpisodeWant{2: {SeriesID: 5, Season: 1}, 4: {SeriesID: 5, Season: 2}}
	if err := driver.SaveStates(map[string]*State{"s": {Follow: map[int]bool{}, Wanted: wanted}}); err != nil {
		t.Fatal(err)
	}

	// Dry run: nothing asked of the indexers; the season is counted, from
	// what a dry run of Take found.
	driver.preview = map[string]*State{"s": {Follow: map[int]bool{}, Wanted: map[int]EpisodeWant{2: {SeriesID: 5, Season: 1}}}}
	used, waiting, err := driver.ReplaceSeasons(context.Background(), cfg, 3, true, false)
	if err != nil || used != 0 {
		t.Fatalf("dry run: %d searches, %v", used, err)
	}
	if waiting != 1 {
		t.Fatalf("dry run counted %d seasons", waiting)
	}
	if changes := fake.Mutations(); len(changes) != 0 {
		t.Fatalf("dry run changed: %v", changes)
	}

	used, _, err = driver.ReplaceSeasons(context.Background(), cfg, 3, false, false)
	if err != nil || used != 1 {
		t.Fatalf("%d searches, %v", used, err)
	}
	if !slices.Contains(fake.Mutations(), "sonarr POST /api/v3/release") || !slices.Contains(fake.Mutations(), "grab pack") {
		t.Fatalf("grabbed: %v", fake.Mutations())
	}
	if _, err := driver.SearchEpisodes(context.Background(), cfg, 3, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.SearchEpisodes(context.Background(), cfg, 3, false, false); err != nil {
		t.Fatal(err)
	}
	var searched []string
	for _, change := range fake.Mutations() {
		if strings.HasPrefix(change, "search") {
			searched = append(searched, change)
		}
	}
	if !slices.Equal(searched, []string{"search episode 4"}) {
		t.Fatalf("searched one by one: %v", searched)
	}

	// Downloaded; Sonarr took episode 2 (it had no file) and blocked the rest.
	fake.Mu.Lock()
	fake.Queue = []map[string]any{{"downloadId": "D1", "title": "pack", "seriesId": 5, "seasonNumber": 1, "status": "completed", "trackedDownloadState": "importBlocked"}}
	fake.Files[12] = map[string]any{"id": 12, "seasonNumber": 1, "dateAdded": arrfake.ISO(now.Add(time.Hour))}
	fake.Eps[2]["episodeFileId"], fake.Eps[2]["hasFile"] = 12, true
	item := func(path string, episode int) map[string]any {
		return map[string]any{"path": path, "seasonNumber": 1, "episodes": []map[string]any{{"id": episode}}, "quality": map[string]any{}, "languages": []any{}, "downloadId": "D1"}
	}
	fake.ImportAt = []map[string]any{item("/dl/e1.mkv", 1), item("/dl/e2.mkv", 2), item("/dl/e3.mkv", 3)}
	fake.Mu.Unlock()
	driver.now = func() time.Time { return now.Add(2 * time.Hour) }
	if _, _, err := driver.ReplaceSeasons(context.Background(), cfg, 3, false, true); err != nil {
		t.Fatal(err)
	}
	fake.Mu.Lock()
	imported := slices.Clone(fake.Imported)
	fake.Mu.Unlock()
	if len(imported) != 1 || !slices.Equal(imported[0], []string{"/dl/e1.mkv", "/dl/e3.mkv"}) {
		t.Fatalf("imported: %v", imported)
	}
	states, _ := driver.States()
	if pack := states["s"].Seasons[seasonKey(5, 1)]; pack == nil || !pack.Done {
		t.Fatalf("season 1: %+v", pack)
	}
	if _, there := states["s"].Wanted[2]; there {
		t.Fatalf("episode 2 still wanted")
	}
	if _, there := states["s"].Wanted[4]; !there {
		t.Fatalf("episode 4 no longer wanted")
	}
}

// A pack that leaves the queue without its files counts as not found, and
// is tried again later; nothing was replaced.
func TestReplaceSeasonsPackThatDidntCome(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	fake.Eps[1] = map[string]any{"id": 1, "seriesId": 5, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": arrfake.ISO(now.AddDate(0, 0, -30)), "hasFile": false, "episodeFileId": 0, "monitored": false}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	pack := &SeasonPack{SeriesID: 5, Season: 1, Release: "pack", GrabbedAt: now.Add(-2 * importWait)}
	if err := driver.SaveStates(map[string]*State{"s": {Wanted: map[int]EpisodeWant{1: {SeriesID: 5, Season: 1}}, Seasons: map[string]*SeasonPack{seasonKey(5, 1): pack}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := driver.ReplaceSeasons(context.Background(), cfg, 0, false, true); err != nil {
		t.Fatal(err)
	}
	states, _ := driver.States()
	got := states["s"].Seasons[seasonKey(5, 1)]
	if got.Done || got.Tries != 1 || !got.GrabbedAt.IsZero() || !got.NextTry.After(now) {
		t.Fatalf("after the pack didn't come: %+v", got)
	}
	if len(fake.Imported) != 0 {
		t.Fatalf("imported: %v", fake.Imported)
	}
}

// A season pack waits while its series' disk has no room, without counting
// as a try, and goes once there's room again.
func TestReplaceSeasonsWaitsForRoom(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	fake.Series[5] = map[string]any{"id": 5, "title": "Full", "path": "/data/Series/Full", "monitored": true, "seasons": []any{}, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
	fake.Eps[1] = map[string]any{"id": 1, "seriesId": 5, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": arrfake.ISO(now.AddDate(0, 0, -30)), "hasFile": false, "monitored": false}
	fake.Releases[1] = []map[string]any{{"guid": "pack", "indexerId": 1, "title": "pack", "fullSeason": true, "seasonNumber": 1, "downloadAllowed": true}}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	if err := driver.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	room := "Overtide says its disk has no room (critical)"
	var asked []string
	driver.Room = func(_ config.Config, folder string) string {
		asked = append(asked, folder)
		return room
	}
	if err := driver.SaveStates(map[string]*State{"s": {Follow: map[int]bool{}, Wanted: map[int]EpisodeWant{1: {SeriesID: 5, Season: 1}}}}); err != nil {
		t.Fatal(err)
	}
	if used, _, err := driver.ReplaceSeasons(context.Background(), cfg, 3, false, false); err != nil || used != 0 {
		t.Fatalf("on a full disk: %d searches, %v", used, err)
	}
	if !slices.Equal(asked, []string{"/data/Series/Full"}) || slices.Contains(fake.Mutations(), "grab pack") {
		t.Fatalf("asked %v, changed %v", asked, fake.Mutations())
	}
	if states, _ := driver.States(); states["s"].Seasons[seasonKey(5, 1)].Tries != 0 {
		t.Fatalf("a full disk counted as a try: %+v", states["s"].Seasons[seasonKey(5, 1)])
	}
	room = ""
	if used, _, err := driver.ReplaceSeasons(context.Background(), cfg, 3, false, false); err != nil || used != 1 || !slices.Contains(fake.Mutations(), "grab pack") {
		t.Fatalf("with room: %d searches, %v, %v", used, err, fake.Mutations())
	}
}

// Past a disk's critical, a followed series' new episodes wait: none stays
// monitored, or Sonarr's RSS keeps grabbing them.
func TestAiringWindowWaitsOnAFullDisk(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	fake.Series[5] = map[string]any{"id": 5, "title": "Airing", "path": "/data/Series/Airing", "monitored": true, "seasons": []any{}}
	fake.Eps[1] = map[string]any{"id": 1, "seriesId": 5, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": arrfake.ISO(now.Add(time.Hour)), "hasFile": false, "monitored": false}
	driver, _ := testDriver(t, fake, config.ManageAll)
	client, err := driver.inv.SonarrClient("s")
	if err != nil {
		t.Fatal(err)
	}
	state := &State{Follow: map[int]bool{5: true}, Wanted: map[int]EpisodeWant{}}
	full := true
	window := func() int {
		n, err := driver.airingWindow(client, state, map[string]bool{}, map[string]bool{}, "s", false, func(int) bool { return full })
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := window(); n != 0 || fake.Eps[1]["monitored"] != false {
		t.Errorf("full: %d monitored, episode %v", n, fake.Eps[1]["monitored"])
	}
	full = false
	if n := window(); n != 1 || fake.Eps[1]["monitored"] != true {
		t.Errorf("with room: %d monitored, episode %v", n, fake.Eps[1]["monitored"])
	}
	full = true
	if n := window(); n != 0 || fake.Eps[1]["monitored"] != false {
		t.Errorf("full again: %d monitored, episode %v", n, fake.Eps[1]["monitored"])
	}
}

// A disk check can't be switched off by a failure: a series whose folder
// nobody can say counts as full.
func TestFullDiskWhenSonarrCantSay(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	driver, cfg := testDriver(t, arrfake.New(now), config.ManageAll)
	driver.Room = func(config.Config, string) string { return "" }
	if !driver.fullDisk(cfg, "nobody")(5) {
		t.Error("an unknown service's series let through")
	}
}
