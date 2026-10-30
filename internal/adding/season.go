package adding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// SeasonWait is how long after a season's last episode aired Overtide
// replaces the season with a whole-season pack: packs usually come out by
// then, and one release for the whole season keeps its episodes alike.
const SeasonWait = 7 * 24 * time.Hour

// importWait is how long a pack sent to the download client may be missing
// from Sonarr's queue before it counts as gone: the queue catches up.
const importWait = time.Hour

// seasonPack is a season Overtide replaces with a whole-season pack.
type seasonPack struct {
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
}

func seasonKey(seriesID, season int) string { return fmt.Sprintf("%d/%d", seriesID, season) }

// pending reports whether the season still waits for its pack, so its
// episodes aren't searched one by one: until a pack came, or MaxTries
// went by without one.
func (pack *seasonPack) pending() bool {
	return pack != nil && !pack.Done && pack.Tries < MaxTries
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

// replaceSeasons replaces with a whole-season pack each season that has
// wanted episodes, SeasonWait after its last episode aired: one search a
// season (within budget), the first complete pack Sonarr would take if it
// weren't for the files already there; once it's downloaded and Sonarr
// imported what it would, the rest of it is imported in place of the old
// files (Sonarr replaces them). A pack not found, or that didn't come, is
// tried again later (retryDelay); nothing is replaced before the pack is
// there. It returns the searches it asked for. In dry run, it only counts.
func (service *Service) replaceSeasons(ctx context.Context, cfg config.Config, budget int, dryRun bool) (int, error) {
	states, err := service.episodeStates()
	if err != nil {
		return 0, err
	}
	if dryRun && service.preview != nil {
		states, service.preview = service.preview, nil
	}
	now := service.now()
	used, waiting := 0, 0
	for _, svc := range cfg.ServicesOfType("sonarr") {
		state := states[svc.ID]
		if state == nil {
			continue
		}
		state = stateFor(states, svc.ID)
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return used, err
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
			if pack.Done || pack.GrabbedAt.IsZero() || dryRun {
				continue
			}
			if err := service.followPack(ctx, client, state, pack, episodes); err != nil {
				return used, err
			}
		}

		// Seasons whose turn it is.
		seasons := map[string]*seasonPack{}
		for _, want := range state.Wanted {
			if want.Season <= 0 {
				continue // specials come one by one
			}
			key := seasonKey(want.SeriesID, want.Season)
			pack := state.Seasons[key]
			if pack == nil {
				pack = &seasonPack{SeriesID: want.SeriesID, Season: want.Season}
			}
			seasons[key] = pack
		}
		for key, pack := range seasons {
			if pack.Done || !pack.GrabbedAt.IsZero() || pack.Tries >= MaxTries {
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
			if dryRun || pack.NextTry.After(now) || budget-used <= 0 || ctx.Err() != nil {
				continue
			}
			used++
			releases, err := client.SeasonReleases(pack.SeriesID, pack.Season)
			var chosen *sonarr.Release
			for i := range releases {
				if acceptablePack(releases[i], pack.Season) {
					chosen = &releases[i]
					break
				}
			}
			if err != nil || chosen == nil {
				pack.Tries++
				pack.NextTry = now.Add(retryDelay(pack.Tries))
				continue
			}
			if err := client.GrabRelease(*chosen); err != nil {
				pack.Tries++
				pack.NextTry = now.Add(retryDelay(pack.Tries))
				continue
			}
			pack.Release, pack.GrabbedAt, pack.DownloadID = chosen.Title, now, ""
		}
		for _, pack := range state.Seasons {
			if !pack.Done && !pack.GrabbedAt.IsZero() {
				waiting++
			}
		}
	}
	if !dryRun {
		if err := service.saveEpisodeStates(states); err != nil {
			return used, err
		}
	}
	if summary, err := service.Summary(); err == nil && !summary.At.IsZero() {
		summary.Seasons = waiting
		_ = service.saveSummary(summary)
	}
	return used, nil
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

// followPack follows a pack sent: downloading, it waits; downloaded and
// past Sonarr's own import, the rest of it is imported in place of the
// season's older files, and the season is the pack's; gone from the queue
// without that, the season is tried again later.
func (service *Service) followPack(ctx context.Context, client *sonarr.Client, state *sonarrState, pack *seasonPack, episodes func(int) ([]sonarr.Episode, error)) error {
	now := service.now()
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
			service.packFailed(pack)
			return nil
		default:
			return nil // Sonarr is still importing what it takes
		}
		return service.importRest(ctx, client, state, pack, episodes)
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
			service.packFailed(pack)
			return nil
		}
	}
	service.packDone(state, pack)
	return nil
}

// importRest imports the pack's files that Sonarr didn't take, in place
// of the season's older files.
func (service *Service) importRest(ctx context.Context, client *sonarr.Client, state *sonarrState, pack *seasonPack, episodes func(int) ([]sonarr.Episode, error)) error {
	items, err := client.ImportItems(pack.DownloadID, pack.SeriesID)
	if err != nil {
		return nil
	}
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
	// An episode is the pack's once its file came after the grab.
	older := map[int]bool{}
	for _, e := range list {
		if e.SeasonNumber == pack.Season && (e.EpisodeFileID == 0 || added[e.EpisodeFileID].Before(pack.GrabbedAt)) {
			older[e.ID] = true
		}
	}
	var rest []sonarr.ImportItem
	for _, item := range items {
		if item.SeasonNumber == nil || *item.SeasonNumber != pack.Season || len(item.Episodes) == 0 {
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
	if len(rest) > 0 {
		if _, err := searchAndCheck(ctx,
			func() (int, error) { return client.Import(pack.SeriesID, rest) },
			client.CommandStatus,
			func() (bool, error) { return true, nil }); err != nil {
			return nil // tried again next run
		}
	}
	service.packDone(state, pack)
	return nil
}

func (service *Service) packDone(state *sonarrState, pack *seasonPack) {
	pack.Done = true
	for id, want := range state.Wanted {
		if want.SeriesID == pack.SeriesID && want.Season == pack.Season {
			delete(state.Wanted, id)
		}
	}
}

func (service *Service) packFailed(pack *seasonPack) {
	pack.Tries++
	pack.NextTry = service.now().Add(retryDelay(pack.Tries))
	pack.Release, pack.GrabbedAt, pack.DownloadID = "", time.Time{}, ""
}
