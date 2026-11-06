package sonarrdriver

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/arr"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/tries"
)

// SeasonWait is how long after a season's last episode aired Overtide
// replaces the season with a whole-season pack: packs usually come out by
// then, and one release for the whole season keeps its episodes alike.
const SeasonWait = 7 * 24 * time.Hour

// FreshSeason is how long after its last episode aired a season counts as
// just finished: people were following it, so its whole-season search
// doesn't wait for the titles a day. Older, it takes its turn after wishes.
const FreshSeason = 30 * 24 * time.Hour

// importWait is how long a pack sent to the download client may be missing
// from Sonarr's queue before it counts as gone: the queue catches up.
const importWait = time.Hour

// SeasonPack is a season Overtide replaces with a whole-season pack.
type SeasonPack struct {
	SeriesID int `json:"seriesId"`
	Season   int `json:"season"`
	// Tries: searches that found no pack, or packs that didn't come.
	Tries   int       `json:"tries,omitempty"`
	NextTry time.Time `json:"nextTry,omitempty"`
	// Release and GrabbedAt: the pack sent; DownloadID, its download once
	// Sonarr's queue has it.
	Release    string    `json:"release,omitempty"`
	GrabbedAt  time.Time `json:"grabbedAt,omitempty"`
	DownloadID string    `json:"downloadId,omitempty"`
	// Done: the season is the pack's.
	Done bool `json:"done,omitempty"`
	// Tried: releases whose files couldn't be told apart, Overtide doing the
	// imports. Others go first.
	Tried []string `json:"tried,omitempty"`
	// LastTry is its last search.
	LastTry time.Time `json:"lastTry,omitzero"`
	// Short: a pack of it was imported and it still lacks that many
	// episodes. It isn't searched for again: it needs a look.
	Short int `json:"short,omitempty"`
	// Unmatched: files of a download in the queue that match none of
	// Sonarr's episodes. Same: it needs a look.
	Unmatched []string `json:"unmatched,omitempty"`
}

func seasonKey(seriesID, season int) string { return fmt.Sprintf("%d/%d", seriesID, season) }

// pending reports whether the season still waits for its pack, so its
// episodes aren't searched one by one: until its first search found none,
// or while one sent is on its way. Packs are still looked for after
// (tries.Max), but its episodes don't wait for them.
func (pack *SeasonPack) pending() bool {
	return pack != nil && !pack.Done && !pack.short() && pack.Tries < tries.Max && (pack.Tries == 0 || !pack.GrabbedAt.IsZero())
}

// short reports whether a pack of the season came and left it needing a
// look: missing episodes, or files that match none.
func (pack *SeasonPack) short() bool {
	return pack != nil && (pack.Short > 0 || len(pack.Unmatched) > 0)
}

// wanted reports whether a season's whole-season search is still to come,
// as far as nothing says otherwise: its episodes aren't searched one by one.
func (pack *SeasonPack) wanted() bool {
	return pack == nil || pack.pending()
}

// acceptablePack reports whether Sonarr refused a release only because
// some episodes have files already (which the pack replaces): anything
// else it refused for, it stays refused.
func acceptablePack(release sonarr.Release, season int) bool {
	if !release.FullSeason || release.SeasonNumber != season || !release.DownloadAllowed {
		return false
	}
	for _, rejection := range release.Rejections {
		if !strings.HasPrefix(rejection, "Existing file") {
			return false
		}
	}
	return true
}

// ReplaceSeasons replaces with a whole-season pack each season that has
// wanted episodes, SeasonWait after its last episode aired: one search a
// season (within budget, seekPack); once it's downloaded and Sonarr
// imported what it would, the rest of it is imported in place of the old
// files (Sonarr replaces them). A pack not found, or that didn't come, is
// tried again later (tries.RetryDelay); nothing is replaced before the pack
// is there. A season complete without one gets its pack as an upgrade
// (PackUpgrades). It takes the seasons just finished, which don't wait for
// the titles a day but count in it, or the older ones, which do, most valued
// first. It returns the searches it asked for, and how many seasons wait
// for their pack or are getting one. In dry run, it only counts.
func (driver *Driver) ReplaceSeasons(ctx context.Context, cfg config.Config, budget int, dryRun, fresh bool) (int, int, error) {
	states, err := driver.States()
	if err != nil {
		return 0, 0, err
	}
	if dryRun && driver.preview != nil {
		states = driver.preview
	}
	now := driver.now()
	used, waiting := 0, 0
	for _, svc := range cfg.ServicesOfType("sonarr") {
		state := states[svc.ID]
		if state == nil {
			continue
		}
		state = stateFor(states, svc.ID)
		client, err := driver.inv.SonarrClient(svc.ID)
		if err != nil {
			return used, waiting, err
		}
		client = client.WithContext(ctx)
		episodesOf := map[int][]sonarr.Episode{}
		episodes := func(seriesID int) ([]sonarr.Episode, error) {
			if cached, ok := episodesOf[seriesID]; ok {
				return cached, nil
			}
			list, err := client.Episodes(seriesID)
			if err == nil {
				episodesOf[seriesID] = list
			}
			return list, err
		}

		// Packs sent: imported in place of the old files once downloaded,
		// or tried again if they didn't come.
		for _, pack := range state.Seasons {
			if !fresh || pack.Done || pack.GrabbedAt.IsZero() || dryRun {
				continue
			}
			if driver.autoImportOff[svc.ID] {
				driver.followImported(client, state, pack)
				continue
			}
			if err := driver.followPack(ctx, client, state, pack, episodes); err != nil {
				return used, waiting, err
			}
		}

		// Seasons whose turn it is.
		seasons := map[string]*SeasonPack{}
		for _, want := range state.Wanted {
			if want.Season <= 0 {
				continue // specials come one by one
			}
			key := seasonKey(want.SeriesID, want.Season)
			pack := state.Seasons[key]
			if pack == nil {
				pack = &SeasonPack{SeriesID: want.SeriesID, Season: want.Season}
			}
			seasons[key] = pack
		}
		full := driver.fullDisk(cfg, svc.ID)
		names := driver.seriesNames(svc.ID)
		keys := slices.Sorted(maps.Keys(seasons))
		if !fresh {
			sort.SliceStable(keys, func(i, j int) bool {
				a, b := seasons[keys[i]], seasons[keys[j]]
				return driver.SeasonValue(ctx, cfg, svc.ID, a.SeriesID, a.Season) > driver.SeasonValue(ctx, cfg, svc.ID, b.SeriesID, b.Season)
			})
		}
		for _, key := range keys {
			pack := seasons[key]
			if pack.Done || pack.short() || !pack.GrabbedAt.IsZero() || pack.Tries >= tries.Max {
				continue
			}
			list, err := episodes(pack.SeriesID)
			if err != nil {
				continue
			}
			if !seasonOut(list, pack.Season, now) {
				continue
			}
			state.Seasons[key] = pack
			waiting++
			if dryRun || seasonFresh(list, pack.Season, now) != fresh || budget-used <= 0 || ctx.Err() != nil || driver.Stopped() {
				continue
			}
			if pack.NextTry.After(now) {
				// No pack last time: its episodes not searched yet go now.
				if !pack.pending() && !full(pack.SeriesID) {
					n, err := driver.seasonEpisodes(ctx, client, state, pack, budget-used, names[pack.SeriesID])
					used += n
					if err == nil {
						err = driver.SaveStates(states)
					}
					if err != nil {
						return used, waiting, err
					}
				}
				continue
			}
			if !fresh {
				if !driver.DailyLimit(1) {
					continue
				}
			}
			if !driver.Ready() {
				continue
			}
			// A pack adds to its disk until Sonarr imports it: none on a
			// full disk. It waits, not counted as a try.
			if full(pack.SeriesID) {
				continue
			}
			used++
			driver.seekPack(ctx, cfg, svc.ID, client, state, pack, list, names[pack.SeriesID])
			if err := driver.SaveStates(states); err != nil {
				return used, waiting, err
			}
			if !pack.pending() {
				n, err := driver.seasonEpisodes(ctx, client, state, pack, budget-used, names[pack.SeriesID])
				used += n
				if err == nil {
					err = driver.SaveStates(states)
				}
				if err != nil {
					return used, waiting, err
				}
			}
		}
		for _, pack := range state.Seasons {
			if !pack.Done && !pack.GrabbedAt.IsZero() {
				waiting++
			}
		}
	}
	if !dryRun {
		if err := driver.SaveStates(states); err != nil {
			return used, waiting, err
		}
	}
	return used, waiting, nil
}

// seekPack searches for a whole-season pack of a season and sends the
// first complete one Sonarr would take if it weren't for the files already
// there. None found, it's a try (unless the trackers weren't all there),
// and the season's wanted episodes get, from the same search, each the
// first single release Sonarr would take as it is: they needn't wait for a
// pack nobody made yet (the others: seasonEpisodes). Their files aren't
// read first: Sonarr matched each to its episode.
func (driver *Driver) seekPack(ctx context.Context, cfg config.Config, serviceID string, client *sonarr.Client, state *State, pack *SeasonPack, list []sonarr.Episode, series string) {
	now := driver.now()
	driver.Step(seasonName(series, pack.Season), "Sonarr is searching for the whole season")
	pack.LastTry = now
	started := time.Now()
	releases, err := client.SeasonReleases(pack.SeriesID, pack.Season)
	var chosen *sonarr.Release
	if driver.autoImportOff[serviceID] {
		chosen = driver.chooseRelease(ctx, releases, pack, list, driver.episodeOrders(ctx, cfg, driver.tmdbOf(client, pack.SeriesID)))
	} else {
		for i := range releases {
			if acceptablePack(releases[i], pack.Season) {
				chosen = &releases[i]
				break
			}
		}
	}
	if err == nil && chosen != nil {
		if err := client.GrabRelease(*chosen); err != nil {
			pack.Tries++
			pack.NextTry = now.Add(tries.RetryDelay(pack.Tries))
			return
		}
		pack.Release, pack.GrabbedAt, pack.DownloadID = chosen.Title, now, ""
		if err := driver.Started(1); err != nil {
			log.Printf("[adding] count the download of %s: %v", chosen.Title, err)
		}
		return
	}
	if down, _ := driver.Unavailable(started); down {
		pack.NextTry = now.Add(tries.LaterOnError) // not a try: the trackers weren't all there
		return
	}
	pack.Tries++
	pack.NextTry = now.Add(tries.RetryDelay(pack.Tries))
	if err != nil {
		return
	}
	for _, release := range singleEpisodes(releases, pack, state.Wanted) {
		if err := client.GrabRelease(release); err != nil {
			log.Printf("[adding] send %s: %v", release.Title, err)
			continue
		}
		for _, e := range release.Episodes {
			want := state.Wanted[e.ID]
			want.Grabbed, want.LastTry, want.Result = true, now, "Downloading"
			state.Wanted[e.ID] = want
		}
		// Not counted in the titles a day: the season's search was.
	}
}

// seasonEpisodes searches one by one, at most budget of them, for the
// wanted episodes of a season its pack search didn't bring, those not
// searched yet: they needn't wait their turn among the other missing
// episodes. Not counted in the titles a day: the season's search was. It
// returns the searches it asked for.
func (driver *Driver) seasonEpisodes(ctx context.Context, client *sonarr.Client, state *State, pack *SeasonPack, budget int, series string) (int, error) {
	var due []int
	for id, want := range state.Wanted {
		if want.SeriesID == pack.SeriesID && want.Season == pack.Season && !want.Grabbed && want.LastTry.IsZero() {
			due = append(due, id)
		}
	}
	slices.Sort(due)
	used := 0
	for _, id := range due {
		if used >= budget || ctx.Err() != nil || driver.Stopped() || !driver.Ready() {
			break
		}
		episode, err := client.GetEpisode(id)
		if arr.IsNotFound(err) || (err == nil && episode.HasFile) {
			delete(state.Wanted, id)
			continue
		}
		if err != nil {
			continue
		}
		used++
		driver.searchWanted(ctx, client, state, id, series)
	}
	return used, ctx.Err()
}

// singleEpisodes picks, from a season search's releases (Sonarr's best
// first), one for each wanted episode of the season not on its way yet:
// the first Sonarr would take as it is, of that season's episodes only,
// all of them wanted and none picked already.
func singleEpisodes(releases []sonarr.Release, pack *SeasonPack, wanted map[int]EpisodeWant) []sonarr.Release {
	taken := map[int]bool{}
	var out []sonarr.Release
	for _, release := range releases {
		if release.FullSeason || !release.Approved || !release.DownloadAllowed || release.SeasonNumber != pack.Season || len(release.Episodes) == 0 {
			continue
		}
		ok := true
		for _, e := range release.Episodes {
			want, isWanted := wanted[e.ID]
			if !isWanted || want.Grabbed || want.SeriesID != pack.SeriesID || want.Season != pack.Season || taken[e.ID] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for _, e := range release.Episodes {
			taken[e.ID] = true
		}
		out = append(out, release)
	}
	return out
}

// seasonOut reports whether every episode of the season aired SeasonWait
// ago.
func seasonOut(episodes []sonarr.Episode, season int, now time.Time) bool {
	found := false
	for _, e := range episodes {
		if e.SeasonNumber != season {
			continue
		}
		found = true
		if e.AirDateUTC.IsZero() || e.AirDateUTC.After(now.Add(-SeasonWait)) {
			return false
		}
	}
	return found
}

// seasonFresh reports whether the season's last episode aired within
// FreshSeason.
func seasonFresh(episodes []sonarr.Episode, season int, now time.Time) bool {
	for _, e := range episodes {
		if e.SeasonNumber == season && e.AirDateUTC.After(now.Add(-FreshSeason)) {
			return true
		}
	}
	return false
}

// followPack follows a pack sent: downloading, it waits; downloaded and
// past Sonarr's own import, the rest of it is imported in place of the
// season's older files, and the season is the pack's; gone from the queue
// without that, the season is tried again later.
func (driver *Driver) followPack(ctx context.Context, client *sonarr.Client, state *State, pack *SeasonPack, episodes func(int) ([]sonarr.Episode, error)) error {
	now := driver.now()
	queue, err := client.Queue(pack.SeriesID)
	if err != nil {
		return nil // asked again next run
	}
	var item *sonarr.QueueItem
	for i := range queue {
		if (pack.DownloadID != "" && queue[i].DownloadID == pack.DownloadID) || (pack.DownloadID == "" && queue[i].Title == pack.Release) {
			item = &queue[i]
			break
		}
	}
	if item != nil {
		pack.DownloadID = item.DownloadID
		if item.Status != "completed" {
			return nil
		}
		switch item.TrackedDownloadState {
		case "importBlocked", "imported":
		case "failed", "failedPending":
			driver.packFailed(pack)
			return nil
		default:
			return nil // Sonarr is still importing what it takes
		}
		return driver.importRest(ctx, client, state, pack, episodes)
	}
	if now.Sub(pack.GrabbedAt) < importWait {
		return nil
	}
	// Gone from the queue: Sonarr imported all of it, or it failed.
	list, err := episodes(pack.SeriesID)
	if err != nil {
		return nil
	}
	files, err := client.EpisodeFiles(pack.SeriesID)
	if err != nil {
		return nil
	}
	added := map[int]time.Time{}
	for _, f := range files {
		added[f.ID] = f.DateAdded
	}
	for _, e := range list {
		if e.SeasonNumber == pack.Season && (e.EpisodeFileID == 0 || added[e.EpisodeFileID].Before(pack.GrabbedAt)) {
			driver.packFailed(pack)
			return nil
		}
	}
	driver.packDone(state, pack)
	return nil
}

// importRest imports the pack's files that Sonarr didn't take, in place
// of the season's older files.
func (driver *Driver) importRest(ctx context.Context, client *sonarr.Client, state *State, pack *SeasonPack, episodes func(int) ([]sonarr.Episode, error)) error {
	if _, err := driver.replaceOlder(ctx, client, pack.DownloadID, pack.SeriesID, pack.Season, pack.GrabbedAt, episodes); err != nil {
		return nil // tried again next run
	}
	driver.packDone(state, pack)
	return nil
}

// replaceOlder imports a download's files for a season in place of the
// episodes' files older than since (Sonarr replaces them, whatever it thinks
// of them), so a season comes from one release. It returns how many files
// it imported. An error means nothing's sure: it's to be tried again.
func (driver *Driver) replaceOlder(ctx context.Context, client *sonarr.Client, downloadID string, seriesID, season int, since time.Time, episodes func(int) ([]sonarr.Episode, error)) (int, error) {
	items, err := client.ImportItems(downloadID, seriesID)
	if err != nil {
		return 0, err
	}
	list, err := episodes(seriesID)
	if err != nil {
		return 0, err
	}
	files, err := client.EpisodeFiles(seriesID)
	if err != nil {
		return 0, err
	}
	added := map[int]time.Time{}
	for _, f := range files {
		added[f.ID] = f.DateAdded
	}
	// An episode is the download's once its file came after that.
	older := map[int]bool{}
	for _, e := range list {
		if e.SeasonNumber == season && (e.EpisodeFileID == 0 || added[e.EpisodeFileID].Before(since)) {
			older[e.ID] = true
		}
	}
	// A download Sonarr no longer tracks gives the series' own files instead
	// of its own: those are never imported over themselves.
	library, err := client.SeriesPath(seriesID)
	if err != nil {
		return 0, err
	}
	if library != "" {
		library = strings.TrimSuffix(library, "/") + "/"
	}
	var rest []sonarr.ImportItem
	for _, item := range items {
		if item.SeasonNumber == nil || *item.SeasonNumber != season || len(item.Episodes) == 0 {
			continue
		}
		if library != "" && strings.HasPrefix(item.Path, library) {
			continue
		}
		replaces := false
		for _, e := range item.Episodes {
			if older[e.ID] {
				replaces = true
			}
		}
		if replaces {
			rest = append(rest, item)
		}
	}
	if len(rest) == 0 {
		return 0, nil
	}
	if _, err := tries.SearchAndCheck(ctx,
		func() (int, error) { return client.Import(seriesID, rest) },
		client.CommandStatus,
		func() (bool, error) { return true, nil }); err != nil {
		return 0, err
	}
	return len(rest), nil
}

func (driver *Driver) packDone(state *State, pack *SeasonPack) {
	pack.Done = true
	for id, want := range state.Wanted {
		if want.SeriesID == pack.SeriesID && want.Season == pack.Season {
			delete(state.Wanted, id)
		}
	}
}

func (driver *Driver) packFailed(pack *SeasonPack) {
	pack.Tries++
	pack.NextTry = driver.now().Add(tries.RetryDelay(pack.Tries))
	pack.Release, pack.GrabbedAt, pack.DownloadID = "", time.Time{}, ""
}
