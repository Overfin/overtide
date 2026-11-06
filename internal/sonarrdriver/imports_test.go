package sonarrdriver

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/overfin"
)

// file is a download's file as Sonarr's manual import lists it.
func file(path string, season int, guessed ...int) map[string]any {
	var episodes []map[string]any
	for _, id := range guessed {
		episodes = append(episodes, map[string]any{"id": id})
	}
	return map[string]any{"path": path, "seasonNumber": season, "episodes": episodes}
}

func importTestDriver(t *testing.T, now time.Time) (*arrfake.Arrs, *Driver, config.Config) {
	fake := arrfake.New(now)
	fake.AutoImportOff = true
	fake.FullQueue = []map[string]any{}
	fake.Events = map[string][]map[string]any{}
	fake.Downloads = map[string][]map[string]any{}
	fake.AddSeries(1, nil, arrfake.Season(1, 0, 2))
	fake.Series[1]["tmdbId"] = 5
	for i := 1; i <= 2; i++ {
		fake.Eps[100+i] = map[string]any{"id": 100 + i, "seriesId": 1, "seasonNumber": 1, "episodeNumber": i, "title": "Title " + strconv.Itoa(i), "airDateUtc": arrfake.ISO(now.AddDate(-1, 0, 0)), "hasFile": false}
	}
	driver, cfg := testDriver(t, fake, config.ManageAll)
	return fake, driver, cfg
}

func (driver *Driver) knowOrders(tmdbID int, orders *overfin.EpisodeOrders) {
	if driver.ordersCache == nil {
		driver.ordersCache = map[int]cachedOrders{}
	}
	driver.ordersCache[tmdbID] = cachedOrders{orders: orders, at: driver.now()}
}

func TestImportDownloads(t *testing.T) {
	now := *arrfake.Day("2026-11-01")
	fake, driver, cfg := importTestDriver(t, now)
	ctx := context.Background()
	driver.knowOrders(5, &overfin.EpisodeOrders{Seasons: []overfin.OrderSeason{{Season: 1, Episodes: []overfin.OrderEpisode{
		{Episode: 1, AirDate: "2025-01-01", Name: "Title 1"}, {Episode: 2, AirDate: "2025-01-01", Name: "Title 2"}}}}})

	// Grabbed, still downloading: not looked at, though Sonarr would list it.
	fake.Grabs = []map[string]any{
		{"downloadId": "D", "seriesId": 1, "sourceTitle": "Show.S01", "date": arrfake.ISO(now.Add(-time.Hour))},
		{"downloadId": "D", "seriesId": 1, "sourceTitle": "Show.S01", "date": arrfake.ISO(now.Add(-time.Hour))},
	}
	fake.FullQueue = []map[string]any{{"downloadId": "D", "status": "downloading"}}
	fake.Downloads["D"] = []map[string]any{file("/dl/Show.S01/Show.S01E01.mkv", 1, 101), file("/dl/Show.S01/Show.S01E02.mkv", 1, 102)}
	driver.ImportDownloads(ctx, cfg, false)
	if len(fake.Imported) != 0 {
		t.Fatalf("still downloading: %v", fake.Imported)
	}

	// Gone from the queue, but Sonarr has no list yet (a restart, say).
	fake.FullQueue = []map[string]any{}
	saved := fake.Downloads["D"]
	delete(fake.Downloads, "D")
	driver.ImportDownloads(ctx, cfg, false)
	states, _ := driver.States()
	if d := states["s"].Imports["D"]; len(fake.Imported) != 0 || d == nil || d.Done {
		t.Fatalf("no list yet: %v, %+v", fake.Imported, d)
	}
	if !states["s"].importing(1) {
		t.Error("not seen as being imported")
	}

	// Dry run: nothing, even with a list.
	fake.Downloads["D"] = saved
	driver.ImportDownloads(ctx, cfg, true)
	if len(fake.Imported) != 0 {
		t.Fatalf("dry run: %v", fake.Imported)
	}

	// Listed: imported by number, the one order being clear.
	driver.ImportDownloads(ctx, cfg, false)
	if len(fake.Imported) != 1 || len(fake.Imported[0]) != 2 || !slices.Equal(fake.ImportedTo["/dl/Show.S01/Show.S01E02.mkv"], []int{102}) {
		t.Fatalf("imported: %v %v", fake.Imported, fake.ImportedTo)
	}

	// Sonarr's history shows the first file only: the other's asked again,
	// and the download isn't over.
	imported := func(path string) map[string]any {
		return map[string]any{"eventType": "downloadFolderImported", "date": arrfake.ISO(now), "downloadId": "D", "data": map[string]any{"droppedPath": path}}
	}
	fake.Events["D"] = []map[string]any{imported("/dl/Show.S01/Show.S01E01.mkv")}
	fake.Imported = nil
	driver.ImportDownloads(ctx, cfg, false)
	if len(fake.Imported) != 1 || !slices.Equal(fake.Imported[0], []string{"/dl/Show.S01/Show.S01E02.mkv"}) {
		t.Fatalf("the rest: %v", fake.Imported)
	}

	// All in: over, and never asked again.
	fake.Events["D"] = append(fake.Events["D"], imported("/dl/Show.S01/Show.S01E02.mkv"))
	fake.Imported = nil
	driver.ImportDownloads(ctx, cfg, false)
	driver.ImportDownloads(ctx, cfg, false)
	states, _ = driver.States()
	if d := states["s"].Imports["D"]; len(fake.Imported) != 0 || !d.Done || states["s"].importing(1) {
		t.Fatalf("over: %v, %+v", fake.Imported, d)
	}
}

// Files nothing can tell wait, and go in once something can.
func TestImportWaits(t *testing.T) {
	now := *arrfake.Day("2026-11-01")
	fake, driver, cfg := importTestDriver(t, now)
	ctx := context.Background()
	fake.Grabs = []map[string]any{{"downloadId": "D", "seriesId": 1, "sourceTitle": "Show.S01", "date": arrfake.ISO(now.Add(-time.Hour))}}
	fake.Downloads["D"] = []map[string]any{file("/dl/p/Show.S01E01.mkv", 1, 101), file("/dl/p/Show.S01E02.Title.1.mkv", 1, 102)}

	// Nothing to go by but a title: that one goes in, on the episode it names.
	driver.ImportDownloads(ctx, cfg, false)
	if !slices.Equal(fake.ImportedTo["/dl/p/Show.S01E02.Title.1.mkv"], []int{101}) || fake.ImportedTo["/dl/p/Show.S01E01.mkv"] != nil {
		t.Fatalf("title: %v", fake.ImportedTo)
	}
	states, _ := driver.States()
	d := states["s"].Imports["D"]
	if len(d.Waiting) != 1 || d.Waiting[0].Path != "/dl/p/Show.S01E01.mkv" || d.Done || states["s"].importing(1) {
		t.Fatalf("waiting: %+v", d)
	}

	// The waiting file's number can be believed now, once the other's gone
	// from the conflict: Overfin answers.
	fake.Events["D"] = []map[string]any{{"eventType": "downloadFolderImported", "date": arrfake.ISO(now), "data": map[string]any{"droppedPath": "/dl/p/Show.S01E02.Title.1.mkv"}}}
	fake.Downloads["D"] = []map[string]any{file("/dl/p/Show.S01E01.mkv", 1, 102), file("/dl/p/Show.S01E02.Title.1.mkv", 1, 102)}
	driver.knowOrders(5, &overfin.EpisodeOrders{Seasons: []overfin.OrderSeason{{Season: 1, Episodes: []overfin.OrderEpisode{
		{Episode: 1, AirDate: "2025-01-01", Name: "Title 1"}, {Episode: 2, AirDate: "2025-01-01", Name: "Title 2"}}}}})
	driver.ImportDownloads(ctx, cfg, false)
	if !slices.Equal(fake.ImportedTo["/dl/p/Show.S01E01.mkv"], []int{102}) {
		t.Fatalf("after Overfin: %v", fake.ImportedTo)
	}
}

// Sonarr's own imports aren't redone when auto-import goes off, its failed
// downloads are left, and back on, Overtide lets go.
func TestImportHandover(t *testing.T) {
	now := *arrfake.Day("2026-11-01")
	fake, driver, cfg := importTestDriver(t, now)
	ctx := context.Background()
	fake.Grabs = []map[string]any{
		{"downloadId": "old", "seriesId": 1, "sourceTitle": "Old", "date": arrfake.ISO(now.AddDate(0, 0, -3))},
		{"downloadId": "bad", "seriesId": 1, "sourceTitle": "Bad", "date": arrfake.ISO(now.AddDate(0, 0, -3))},
	}
	fake.Events["old"] = []map[string]any{{"eventType": "downloadFolderImported", "date": arrfake.ISO(now.AddDate(0, 0, -2)), "data": map[string]any{"droppedPath": "/dl/old/a.mkv"}}}
	fake.Events["bad"] = []map[string]any{{"eventType": "downloadFailed", "date": arrfake.ISO(now.AddDate(0, 0, -2))}}
	fake.Downloads["old"] = []map[string]any{file("/dl/old/a.mkv", 1, 101), file("/dl/old/b.mkv", 1, 102)}
	fake.Downloads["bad"] = []map[string]any{file("/dl/bad/a.mkv", 1, 101)}
	driver.ImportDownloads(ctx, cfg, false)
	states, _ := driver.States()
	if len(fake.Imported) != 0 || !states["s"].Imports["old"].Done || !states["s"].Imports["bad"].Failed {
		t.Fatalf("%v, %+v", fake.Imported, states["s"].Imports)
	}
	if !driver.autoImportOff["s"] {
		t.Error("not seen as off")
	}

	fake.AutoImportOff = false
	driver.ImportDownloads(ctx, cfg, false)
	states, _ = driver.States()
	if driver.autoImportOff["s"] || states["s"].Imports != nil || !states["s"].ImportsOffSince.IsZero() {
		t.Fatalf("back on: %v %+v", driver.autoImportOff, states["s"])
	}
}

// A season's pack, with Overtide importing: it waits until its files are in,
// and a pack whose files can't be told apart gives way to another later.
func TestPackFollowedThroughImports(t *testing.T) {
	now := *arrfake.Day("2026-11-01")
	fake, driver, cfg := importTestDriver(t, now)
	state := &State{Imports: map[string]*downloadImport{}, Seasons: map[string]*SeasonPack{}, Wanted: map[int]EpisodeWant{}}
	client, _ := driver.inv.SonarrClient("s")
	_ = cfg
	pack := &SeasonPack{SeriesID: 1, Season: 1, Release: "Show.S01", GrabbedAt: now.Add(-2 * time.Minute)}

	// In the queue: going. It also learns the download's ID.
	fake.Queue = []map[string]any{{"downloadId": "D", "title": "Show.S01", "status": "downloading"}}
	driver.followImported(client, state, pack)
	if pack.DownloadID != "D" || pack.GrabbedAt.IsZero() {
		t.Fatalf("going: %+v", pack)
	}
	fake.Queue = nil

	// Off the queue, not read yet: waits, within reason.
	driver.followImported(client, state, pack)
	if pack.GrabbedAt.IsZero() {
		t.Fatalf("not read yet: %+v", pack)
	}

	// Importing: waits.
	state.Imports["D"] = &downloadImport{SeriesID: 1, Title: "Show.S01"}
	driver.followImported(client, state, pack)
	if pack.GrabbedAt.IsZero() || pack.Done {
		t.Fatalf("importing: %+v", pack)
	}

	// Files can't be told apart: another is tried later.
	state.Imports["D"].Waiting = []waitingFile{{Path: "a", Why: "b"}}
	driver.followImported(client, state, pack)
	if !pack.GrabbedAt.IsZero() || pack.Tries != 1 || pack.Done {
		t.Fatalf("waiting: %+v", pack)
	}

	// All in: the season is the pack's.
	pack = &SeasonPack{SeriesID: 1, Season: 1, Release: "Show.S01", GrabbedAt: now, DownloadID: "D"}
	state.Imports["D"] = &downloadImport{SeriesID: 1, Title: "Show.S01", Done: true}
	state.Wanted[101] = EpisodeWant{SeriesID: 1, Season: 1}
	driver.followImported(client, state, pack)
	if !pack.Done || len(state.Wanted) != 0 {
		t.Fatalf("done: %+v %+v", pack, state.Wanted)
	}

	// Never showed up: tried again after the wait.
	pack = &SeasonPack{SeriesID: 1, Season: 1, Release: "Other", GrabbedAt: now.Add(-2 * importWait)}
	driver.followImported(client, state, pack)
	if !pack.GrabbedAt.IsZero() || pack.Tries != 1 {
		t.Fatalf("never came: %+v", pack)
	}
}

// The operator's word goes over Overtide's: a waiting file goes where they
// say, on another release's turn, or is left alone for good.
func TestImportChoices(t *testing.T) {
	now := *arrfake.Day("2026-11-01")
	fake, driver, cfg := importTestDriver(t, now)
	ctx := context.Background()
	fake.Grabs = []map[string]any{
		{"downloadId": "A", "seriesId": 1, "sourceTitle": "Show.S01-A", "date": arrfake.ISO(now.Add(-time.Hour))},
		{"downloadId": "B", "seriesId": 1, "sourceTitle": "Show.S01-B", "date": arrfake.ISO(now.Add(-time.Hour))},
		{"downloadId": "C", "seriesId": 1, "sourceTitle": "Show.S01-C", "date": arrfake.ISO(now.Add(-time.Hour))},
	}
	for _, id := range []string{"A", "B", "C"} {
		fake.Downloads[id] = []map[string]any{file("/dl/"+id+"/Show.S01E01.mkv", 1, 101), file("/dl/"+id+"/Show.S01E02.mkv", 1, 102)}
	}
	// Nothing to go by: all wait.
	driver.ImportDownloads(ctx, cfg, false)
	waiting, err := driver.WaitingImports()
	if err != nil || len(waiting) != 3 || len(fake.Imported) != 0 {
		t.Fatalf("waiting: %+v, %v, %v", waiting, err, fake.Imported)
	}
	options, guesses, err := driver.ImportOptions(ctx, "s", "A")
	if err != nil || len(options) != 2 || options[0].ID != 101 || options[0].Title != "Title 1" || !slices.Equal(guesses["/dl/A/Show.S01E02.mkv"], []int{102}) {
		t.Fatalf("options: %+v %v %v", options, guesses, err)
	}

	// Refused: nonsense, and no file named.
	if driver.ChooseImport(ImportChoice{ServiceID: "s", DownloadID: "A", Action: "burn"}) == nil {
		t.Error("an unknown action taken")
	}
	if driver.ChooseImport(ImportChoice{ServiceID: "s", DownloadID: "A", Action: ChoiceEpisodes}) == nil {
		t.Error("no files taken")
	}

	// Across what Sonarr guessed; an episode of another series is ignored,
	// so that file stays waiting.
	if err := driver.ChooseImport(ImportChoice{ServiceID: "s", DownloadID: "A", Action: ChoiceEpisodes, Files: map[string][]int{
		"/dl/A/Show.S01E01.mkv": {102}, "/dl/A/Show.S01E02.mkv": {999},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := driver.ChooseImport(ImportChoice{ServiceID: "s", DownloadID: "B", Action: ChoiceAnother}); err != nil {
		t.Fatal(err)
	}
	if err := driver.ChooseImport(ImportChoice{ServiceID: "s", DownloadID: "C", Action: ChoiceIgnore}); err != nil {
		t.Fatal(err)
	}
	driver.ImportDownloads(ctx, cfg, false)
	if !slices.Equal(fake.ImportedTo["/dl/A/Show.S01E01.mkv"], []int{102}) || fake.ImportedTo["/dl/A/Show.S01E02.mkv"] != nil || len(fake.Imported) != 1 {
		t.Fatalf("chosen: %v %v", fake.ImportedTo, fake.Imported)
	}
	states, _ := driver.States()
	imports := states["s"].Imports
	if !imports["B"].Done || !imports["C"].Done || imports["A"].Done || len(imports["A"].Waiting) != 1 {
		t.Fatalf("after the choices: %+v %+v %+v", imports["A"], imports["B"], imports["C"])
	}
	pack := states["s"].Seasons[seasonKey(1, 1)]
	if pack == nil || !slices.Contains(pack.Tried, "Show.S01-B") || pack.Done || pack.NextTry.After(now) || len(states["s"].Wanted) != 2 {
		t.Fatalf("another release: %+v, wanted %+v", pack, states["s"].Wanted)
	}
	// Taken once.
	if left, _ := driver.readChoices(); len(left) != 0 {
		t.Errorf("choices left: %+v", left)
	}
}

// A later release settling the season ends an older one's wait, with nobody
// asked anything.
func TestWaitEndsWhenSeasonSettled(t *testing.T) {
	now := *arrfake.Day("2026-11-01")
	fake, driver, cfg := importTestDriver(t, now)
	ctx := context.Background()
	fake.Grabs = []map[string]any{{"downloadId": "A", "seriesId": 1, "sourceTitle": "Show.S01-A", "date": arrfake.ISO(now.Add(-time.Hour))}}
	fake.Downloads["A"] = []map[string]any{file("/dl/A/Show.S01E01.mkv", 1, 101), file("/dl/A/Show.S01E02.mkv", 1, 102)}
	driver.ImportDownloads(ctx, cfg, false)
	states, _ := driver.States()
	if len(states["s"].Imports["A"].Waiting) != 2 {
		t.Fatalf("waiting: %+v", states["s"].Imports["A"])
	}
	// One old file doesn't settle it.
	fake.Files[1] = map[string]any{"id": 1, "dateAdded": arrfake.ISO(now.AddDate(-1, 0, 0))}
	fake.Files[2] = map[string]any{"id": 2, "dateAdded": arrfake.ISO(now)}
	fake.Eps[101]["episodeFileId"], fake.Eps[102]["episodeFileId"] = 1, 2
	driver.ImportDownloads(ctx, cfg, false)
	states, _ = driver.States()
	if len(states["s"].Imports["A"].Waiting) != 2 {
		t.Fatalf("not settled: %+v", states["s"].Imports["A"])
	}
	fake.Files[1]["dateAdded"] = arrfake.ISO(now)
	driver.ImportDownloads(ctx, cfg, false)
	states, _ = driver.States()
	if d := states["s"].Imports["A"]; !d.Done || len(d.Waiting) != 0 || len(fake.Imported) != 0 {
		t.Fatalf("settled: %+v, %v", d, fake.Imported)
	}
}
