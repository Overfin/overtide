package sonarrdriver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/arr"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/tries"
)

// missingPages bounds how many pages of Sonarr's missing episodes a pass
// reads when it takes series (200 each).
const missingPages = 50

// LookEvery is how often incomplete seasons are looked for.
const LookEvery = 6 * time.Hour

// SearchAfter: an airing episode that hasn't come through the RSS that
// long after it aired is searched for, every day until tries.ReleaseGrace.
const SearchAfter = 2 * 24 * time.Hour

// Taken is what Take did, or would do in dry run, for the "Manage all"
// summary. Tagged: series that got one of adding's tags; SeriesTaken:
// series Sonarr no longer grabs for on its own; EpisodesWanted: missing
// episodes Overtide searches for; Following: series whose new episodes it
// fetches; Monitored: episodes airing now, monitored for Sonarr's RSS.
type Taken struct {
	Tagged, SeriesTaken, EpisodesWanted, Following, Monitored int
}

// Take is "Manage all" in every Sonarr: each series gets one of Overtide's
// tags (labelFor says which, by when it was added), and Sonarr no longer
// grabs anything on its own. What it would have fetched, Overtide fetches
// instead, at its own pace and without upgrades: a monitored missing episode
// goes in the wanted episodes; a series whose new episodes were followed
// keeps them coming, each monitored from tries.ReleaseWindow before it airs
// until it's downloaded (Sonarr's RSS grabs it), then searched for by
// Overtide after tries.ReleaseGrace. What was unmonitored stays unwanted: a
// season nobody wanted. The series in active (service|series|ID) Overtide
// monitors itself for a release: they're left as they are. In dry run,
// nothing changes: Taken says what would. After an error, it says what was
// done until then.
func (driver *Driver) Take(ctx context.Context, cfg config.Config, active map[string]bool, labelFor func(added time.Time) (label string, byHand bool), dryRun bool) (taken Taken, err error) {
	driver.preview = nil
	now := driver.now()
	states, err := driver.States()
	if err != nil {
		return taken, err
	}
	// A failure halfway: what was read and taken so far is kept, or a series
	// already taken is never read again, and its wanted episodes are lost.
	defer func() {
		if err != nil && !dryRun {
			if saveErr := driver.SaveStates(states); saveErr != nil {
				err = errors.Join(err, saveErr)
			}
		}
	}()
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := driver.inv.SonarrClient(svc.ID)
		if err != nil {
			return taken, err
		}
		client = client.WithContext(ctx)
		state := stateFor(states, svc.ID)
		labels, err := client.TagLabels()
		if err != nil {
			return taken, err
		}
		titles, err := client.Titles()
		if err != nil {
			return taken, err
		}
		toTag := map[string][]int{}
		var toTake []sonarr.Title
		// byHand are the series added from outside since "Manage all" (by
		// hand, by an import list or by another app): what they lack is
		// searched first.
		byHand := map[int]bool{}
		for _, t := range titles {
			if model.HasLabel(t.Tags, labels, model.TagIgnore) {
				// Left alone: nothing of it is followed or searched.
				delete(state.Follow, t.ID)
				for id, want := range state.Wanted {
					if want.SeriesID == t.ID {
						delete(state.Wanted, id)
					}
				}
				continue
			}
			label, hand := labelFor(t.Added)
			if !model.HasManagedLabel(t.Tags, labels) {
				toTag[label] = append(toTag[label], t.ID)
			}
			if hand {
				byHand[t.ID] = true
			}
			if !t.Taken() && !active[fmt.Sprintf("%s|%s|%d", svc.ID, model.Series, t.ID)] {
				toTake = append(toTake, t)
			}
		}
		if len(toTake) > 0 {
			// What Sonarr would have fetched for them: its monitored missing
			// episodes, read before they're unmonitored.
			taking := map[int]bool{}
			for _, t := range toTake {
				taking[t.ID] = true
				state.Follow[t.ID] = followsNew(t)
			}
			for page := 1; page <= missingPages; page++ {
				missing, total, err := client.MissingEpisodes(true, page, 200)
				if err != nil {
					return taken, err
				}
				for _, e := range missing {
					// A recent one in a followed series is the airing
					// window's: monitored for the RSS until tries.ReleaseGrace.
					recent := e.AirDateUTC.After(now.Add(-tries.ReleaseGrace))
					if taking[e.SeriesID] && !e.AirDateUTC.IsZero() && !e.AirDateUTC.After(now) && !(recent && state.Follow[e.SeriesID]) {
						if _, there := state.Wanted[e.ID]; !there {
							state.Wanted[e.ID] = EpisodeWant{SeriesID: e.SeriesID, Season: e.SeasonNumber, AiredAt: e.AirDateUTC, Episode: e.EpisodeNumber, ByHand: byHand[e.SeriesID]}
						}
					}
				}
				if len(missing) == 0 || page*200 >= total {
					break
				}
			}
			taken.SeriesTaken += len(toTake)
			if !dryRun {
				// How each was monitored, kept before anything changes: it
				// goes back as it was when given back.
				before := map[string]TakenTitle{}
				for _, t := range toTake {
					m, err := client.ReadMonitoring(t.ID)
					if err != nil {
						return taken, err
					}
					before[TakenKey(svc.ID, model.Series, t.ID)] = TakenTitle{Monitored: m.Monitored, Series: &m}
				}
				if err := driver.RecordTaken(before); err != nil {
					return taken, err
				}
				for _, t := range toTake {
					if err := client.TakeSeries(t.ID); err != nil {
						return taken, err
					}
				}
			}
		}
		for label, ids := range toTag {
			taken.Tagged += len(ids)
			if dryRun {
				continue
			}
			tagID, err := client.EnsureTag(label)
			if err != nil {
				return taken, err
			}
			if err := client.EditTags(ids, tagID, true); err != nil {
				return taken, err
			}
		}
		monitored, err := driver.tendSeries(client, state, titles, labels, active, svc.ID, true, dryRun, driver.fullDisk(cfg, svc.ID))
		if err != nil {
			return taken, err
		}
		taken.Monitored += monitored
		for _, follow := range state.Follow {
			if follow {
				taken.Following++
			}
		}
		taken.EpisodesWanted += len(state.Wanted)
	}
	if dryRun {
		driver.preview = states
	} else if err := driver.SaveStates(states); err != nil {
		return taken, err
	}
	return taken, nil
}

// Tend is what "Only what it adds" does for the series Overtide manages,
// as Take does for all: their airing seasons and incomplete ones. The
// series in active are left as they are, as in Take. A Sonarr that doesn't
// answer is tried again next run.
func (driver *Driver) Tend(ctx context.Context, cfg config.Config, active map[string]bool, dryRun bool) {
	driver.preview = nil
	states, err := driver.States()
	if err != nil {
		log.Printf("[adding] the wanted episodes: %v", err)
		return
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := driver.inv.SonarrClient(svc.ID)
		if err == nil {
			client = client.WithContext(ctx)
			var labels map[int]string
			var titles []sonarr.Title
			if labels, err = client.TagLabels(); err == nil {
				titles, err = client.Titles()
			}
			if err == nil {
				_, err = driver.tendSeries(client, stateFor(states, svc.ID), titles, labels, active, svc.ID, false, dryRun, driver.fullDisk(cfg, svc.ID))
			}
		}
		if err != nil {
			log.Printf("[adding] Sonarr %s's airing and incomplete seasons: %v", svc.Name, err)
		}
	}
	if dryRun {
		driver.preview = states
	} else if err := driver.SaveStates(states); err != nil {
		log.Printf("[adding] the wanted episodes: %v", err)
	}
}

// tendSeries keeps the airing seasons coming and finds incomplete ones, for
// the series Overtide manages (all of them with manageAll). It returns how
// many episodes are monitored.
func (driver *Driver) tendSeries(client *sonarr.Client, state *State, titles []sonarr.Title, labels map[int]string, active map[string]bool, serviceID string, manageAll, dryRun bool, full func(seriesID int) bool) (int, error) {
	var managed []sonarr.Title
	holds := map[string]bool{}
	for _, t := range titles {
		if model.HasLabel(t.Tags, labels, model.TagIgnore) || (!manageAll && !model.HasOwnLabel(t.Tags, labels)) {
			continue
		}
		managed = append(managed, t)
		for _, season := range t.Seasons {
			if season.SeasonNumber > 0 && season.Statistics.EpisodeFileCount > 0 {
				holds[seasonKey(t.ID, season.SeasonNumber)] = true
			}
		}
	}
	monitored, err := driver.airingWindow(client, state, holds, active, serviceID, dryRun, full)
	if err != nil {
		return 0, err
	}
	return monitored, driver.findIncomplete(client, state, managed)
}

// findIncomplete puts in the wanted episodes, every LookEvery, those of the
// seasons that have some of their files but lack episodes that aired. A
// season with no file is one nobody wants, and one that lost a file within
// tries.Cooldown was removed, not missed: neither is touched.
func (driver *Driver) findIncomplete(client *sonarr.Client, state *State, titles []sonarr.Title) error {
	now := driver.now()
	if now.Sub(state.Looked) < LookEvery {
		return nil
	}
	queued, err := client.QueuedEpisodes()
	if err != nil {
		return err
	}
	stillShort := map[string]bool{}
	for _, t := range titles {
		incomplete := map[int]bool{}
		for _, season := range t.Seasons {
			files := season.Statistics.EpisodeFileCount
			if season.SeasonNumber > 0 && files > 0 && files < season.Statistics.TotalEpisodeCount {
				incomplete[season.SeasonNumber] = true
			}
		}
		if len(incomplete) == 0 {
			continue
		}
		episodes, err := client.Episodes(t.ID)
		if err != nil {
			return err
		}
		var lacking []sonarr.Episode
		for _, e := range episodes {
			if incomplete[e.SeasonNumber] && !e.HasFile && !queued[e.ID] && !e.AirDateUTC.IsZero() && e.AirDateUTC.Before(now.Add(-SearchAfter)) {
				lacking = append(lacking, e)
			}
		}
		if len(lacking) == 0 {
			continue
		}
		events, err := client.History(t.ID)
		if err != nil {
			return err
		}
		deleted := sonarr.DeletedSince(events, now.Add(-tries.Cooldown))
		packs := sonarr.PacksImported(events)
		removed := map[int]bool{}
		lastAired := map[int]time.Time{}
		packAt := map[int]time.Time{}
		for _, e := range episodes {
			if deleted[e.ID] {
				removed[e.SeasonNumber] = true
			}
			if e.AirDateUTC.After(lastAired[e.SeasonNumber]) {
				lastAired[e.SeasonNumber] = e.AirDateUTC
			}
			if packs[e.ID].After(packAt[e.SeasonNumber]) {
				packAt[e.SeasonNumber] = packs[e.ID]
			}
		}
		lackingIn := map[int]int{}
		for _, e := range lacking {
			lackingIn[e.SeasonNumber]++
		}
		for number, count := range lackingIn {
			// A pack of the whole season came after its last episode aired,
			// and it's still short: its files don't match Sonarr's episodes,
			// or the pack lacks them. Another search won't help.
			if removed[number] || !packAt[number].After(lastAired[number]) || !seasonOut(episodes, number, now) {
				continue
			}
			key := seasonKey(t.ID, number)
			pack := state.Seasons[key]
			if pack == nil {
				pack = &SeasonPack{SeriesID: t.ID, Season: number}
				state.Seasons[key] = pack
			}
			if !pack.GrabbedAt.IsZero() && !pack.Done {
				continue // its download is being followed
			}
			pack.Short = count
			stillShort[key] = true
			for id, want := range state.Wanted {
				if want.SeriesID == t.ID && want.Season == number {
					delete(state.Wanted, id)
				}
			}
		}
		for _, e := range lacking {
			if removed[e.SeasonNumber] || state.Seasons[seasonKey(t.ID, e.SeasonNumber)].short() {
				continue
			}
			if _, there := state.Wanted[e.ID]; !there {
				state.Wanted[e.ID] = EpisodeWant{SeriesID: t.ID, Season: e.SeasonNumber, AiredAt: e.AirDateUTC, Episode: e.EpisodeNumber}
			}
		}
	}
	// Seasons that were short and aren't any more go back to normal.
	for key, pack := range state.Seasons {
		if pack.Short > 0 && !stillShort[key] {
			pack.Short = 0
		}
	}
	state.Looked = now
	return nil
}

// ClearFinished deals with the finished downloads Sonarr keeps in its queue
// because it refused some of their files. A file matching none of its
// episodes means the numbering may differ: the download stays, and its season
// needs a look. A pack's files it thought no upgrade replace the older ones
// anyway, so the season comes from one release. Then, once every episode
// the download is for has its file, it leaves the queue: nothing's left to
// do, and it would stay for good. The download stays in the client. Those
// Overtide is following, or that it doesn't manage, are left alone.
func (driver *Driver) ClearFinished(ctx context.Context, cfg config.Config, dryRun bool) {
	if dryRun {
		return
	}
	states, err := driver.States()
	if err != nil {
		log.Printf("[adding] the wanted episodes: %v", err)
		return
	}
	items, _, err := driver.inv.Snapshot()
	if err != nil {
		return
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		// Overtide imports, so the queue has nothing finished to clear.
		if driver.autoImportOff[svc.ID] {
			continue
		}
		managed := map[int]bool{}
		for _, m := range items {
			if m.ServiceID == svc.ID && m.Type == model.Series && driver.Managed(m) && !model.HasTag(m.Tags, model.TagIgnore) {
				managed[m.SourceID] = true
			}
		}
		state := stateFor(states, svc.ID)
		followed := map[string]bool{}
		for _, pack := range state.Seasons {
			if !pack.Done && !pack.GrabbedAt.IsZero() {
				followed[pack.DownloadID] = true
			}
		}
		client, err := driver.inv.SonarrClient(svc.ID)
		if err != nil {
			continue
		}
		client = client.WithContext(ctx)
		queue, err := client.FullQueue()
		if err != nil {
			log.Printf("[adding] Sonarr %s's queue: %v", svc.Name, err)
			continue
		}
		episodesOf := map[int][]sonarr.Episode{}
		episodes := func(seriesID int) ([]sonarr.Episode, error) {
			if list, known := episodesOf[seriesID]; known {
				return list, nil
			}
			list, err := client.Episodes(seriesID)
			if err == nil {
				episodesOf[seriesID] = list
			}
			return list, err
		}
		hasFile := func(item sonarr.QueueItem) bool {
			list, err := episodes(item.SeriesID)
			if err != nil {
				return false
			}
			return slices.ContainsFunc(list, func(e sonarr.Episode) bool { return e.ID == item.EpisodeID && e.HasFile })
		}
		downloads := map[string][]sonarr.QueueItem{}
		for _, item := range queue {
			downloads[item.DownloadID] = append(downloads[item.DownloadID], item)
		}
		unmatched := map[string][]string{}
		var ids []int
	download:
		for downloadID, group := range downloads {
			for _, item := range group {
				// Sonarr tried and refused some of it. Without that warning,
				// it may just not have imported it yet: an upgrade, say.
				stuck := item.Status == "completed" && item.TrackedDownloadStatus == "warning" &&
					(item.TrackedDownloadState == "importBlocked" || item.TrackedDownloadState == "importPending")
				if downloadID == "" || !stuck || !managed[item.SeriesID] || followed[downloadID] {
					continue download
				}
			}
			noMatch, noUpgrade := refusals(group)
			if len(noMatch) > 0 {
				for _, item := range group {
					key := seasonKey(item.SeriesID, item.SeasonNumber)
					if !slices.Contains(unmatched[key], noMatch[0]) {
						unmatched[key] = append(unmatched[key], noMatch...)
					}
				}
				continue
			}
			if noUpgrade && len(group) > 1 {
				replaced := 0
				seasons := map[int]bool{}
				failed := false
				for _, item := range group {
					if seasons[item.SeasonNumber] {
						continue
					}
					seasons[item.SeasonNumber] = true
					n, err := driver.replaceOlder(ctx, client, downloadID, item.SeriesID, item.SeasonNumber, item.Added, episodes)
					if err != nil {
						log.Printf("[adding] replacing older files with download %s: %v", downloadID, err)
						failed = true
					}
					replaced += n
				}
				if failed || replaced > 0 {
					// Sonarr's import takes a moment: the next run looks again.
					if replaced > 0 {
						log.Printf("[adding] replaced %d older files of Sonarr %s, so a season comes from one release", replaced, svc.Name)
					}
					delete(episodesOf, group[0].SeriesID)
					continue
				}
			}
			complete := true
			for _, item := range group {
				if !hasFile(item) {
					complete = false
				}
			}
			if complete {
				for _, item := range group {
					ids = append(ids, item.ID)
				}
			}
		}
		driver.noteUnmatched(state, unmatched)
		if err := driver.SaveStates(states); err != nil {
			log.Printf("[adding] the wanted episodes: %v", err)
		}
		if len(ids) == 0 {
			continue
		}
		if err := client.ForgetDownloads(ids); err != nil {
			log.Printf("[adding] clearing finished downloads from Sonarr %s's queue: %v", svc.Name, err)
			continue
		}
		log.Printf("[adding] cleared %d finished episodes from Sonarr %s's queue: every episode has its file", len(ids), svc.Name)
	}
}

// refusals reads why Sonarr refused a download's files: those that match
// none of its episodes, and whether some were no upgrade.
func refusals(group []sonarr.QueueItem) (noMatch []string, noUpgrade bool) {
	for _, item := range group {
		for _, status := range item.StatusMessages {
			for _, message := range status.Messages {
				switch {
				case strings.Contains(message, "Invalid season or episode"):
					if !slices.Contains(noMatch, status.Title) {
						noMatch = append(noMatch, status.Title)
					}
				case strings.Contains(message, "upgrade"):
					noUpgrade = true
				}
			}
		}
	}
	slices.Sort(noMatch)
	return noMatch, noUpgrade
}

// noteUnmatched marks the seasons with files matching none of their episodes:
// they need a look, and aren't searched meanwhile. Those no longer in the
// queue go back to normal.
func (driver *Driver) noteUnmatched(state *State, unmatched map[string][]string) {
	for key, pack := range state.Seasons {
		if _, there := unmatched[key]; !there {
			pack.Unmatched = nil
		}
	}
	for key, names := range unmatched {
		seriesID, season := 0, 0
		if _, err := fmt.Sscanf(key, "%d/%d", &seriesID, &season); err != nil {
			continue
		}
		pack := state.Seasons[key]
		if pack == nil {
			pack = &SeasonPack{SeriesID: seriesID, Season: season}
			state.Seasons[key] = pack
		}
		pack.Unmatched = names
		for id, want := range state.Wanted {
			if want.SeriesID == seriesID && want.Season == season {
				delete(state.Wanted, id)
			}
		}
	}
}

// followsNew reports whether Sonarr would have fetched the series' new
// episodes: it's monitored, and new seasons are, or its last season is.
func followsNew(t sonarr.Title) bool {
	if !t.Monitored {
		return false
	}
	if t.MonitorNewItems == "all" {
		return true
	}
	last, monitored := -1, false
	for _, season := range t.Seasons {
		if season.SeasonNumber > last {
			last, monitored = season.SeasonNumber, season.Monitored
		}
	}
	return monitored
}

// airingWindow monitors, for the followed series and the seasons Overtide
// holds files of (holds, by seasonKey), each episode airing within
// tries.ReleaseWindow without its file, until tries.ReleaseGrace after it
// aired or its download is confirmed, so Sonarr's RSS grabs it; and
// unmonitors the others. One that aired SearchAfter ago without coming goes
// in the wanted episodes too. A series on a disk with no room (full) has none
// monitored: past critical, its new episodes wait. It returns how many
// episodes are monitored.
func (driver *Driver) airingWindow(client *sonarr.Client, state *State, holds, active map[string]bool, serviceID string, dryRun bool, full func(seriesID int) bool) (int, error) {
	now := driver.now()
	episodes, err := client.Calendar(now.Add(-tries.ReleaseGrace-2*24*time.Hour), now.Add(tries.ReleaseWindow))
	if err != nil {
		return 0, err
	}
	queued, err := client.QueuedEpisodes()
	if err != nil {
		return 0, err
	}
	var on, off []int
	monitored := 0
	for _, e := range episodes {
		if (!state.Follow[e.SeriesID] && !holds[seasonKey(e.SeriesID, e.SeasonNumber)]) || active[fmt.Sprintf("%s|%s|%d", serviceID, model.Series, e.SeriesID)] {
			continue
		}
		inWindow := !e.AirDateUTC.IsZero() && !e.AirDateUTC.After(now.Add(tries.ReleaseWindow)) && !e.AirDateUTC.Before(now.Add(-tries.ReleaseGrace))
		want := inWindow && !e.HasFile && !queued[e.ID] && !full(e.SeriesID)
		if want {
			monitored++
		}
		switch {
		case want && !e.Monitored:
			on = append(on, e.ID)
		case !want && e.Monitored:
			off = append(off, e.ID)
		}
		if !e.HasFile && !queued[e.ID] && !e.AirDateUTC.IsZero() && e.AirDateUTC.Before(now.Add(-SearchAfter)) {
			if _, there := state.Wanted[e.ID]; !there {
				state.Wanted[e.ID] = EpisodeWant{SeriesID: e.SeriesID, Season: e.SeasonNumber, AiredAt: e.AirDateUTC, Episode: e.EpisodeNumber}
			}
		}
	}
	if dryRun {
		return monitored, nil
	}
	if err := client.SetEpisodesMonitored(on, true); err != nil {
		return 0, err
	}
	return monitored, client.SetEpisodesMonitored(off, false)
}

// SearchEpisodes searches for the wanted episodes whose turn it is, those
// just aired (within tries.ReleaseGrace, their season still airing) or the
// others, one at a time, at most budget of them: a download started, it's
// followed until imported; nothing found, it's tried again later
// (wantRetry). An episode that has its file, or left Sonarr, leaves the
// list. It returns the searches it asked for.
func (driver *Driver) SearchEpisodes(ctx context.Context, cfg config.Config, budget int, dryRun, airing bool) (int, error) {
	return driver.searchEpisodes(ctx, cfg, budget, dryRun, func(want EpisodeWant, airingNow bool) bool { return !want.ByHand && airingNow == airing })
}

// SearchByHand searches, within budget, for the episodes of the series
// added by hand since "Manage all" (EpisodeWant.ByHand), airing or not:
// before anything else, since the operator wants them now.
func (driver *Driver) SearchByHand(ctx context.Context, cfg config.Config, budget int, dryRun bool) (int, error) {
	return driver.searchEpisodes(ctx, cfg, budget, dryRun, func(want EpisodeWant, _ bool) bool { return want.ByHand })
}

// searchEpisodes searches, within budget, for the wanted episodes pick
// keeps, told whether each is airing.
func (driver *Driver) searchEpisodes(ctx context.Context, cfg config.Config, budget int, dryRun bool, pick func(want EpisodeWant, airing bool) bool) (int, error) {
	states, err := driver.States()
	if err != nil {
		return 0, err
	}
	now := driver.now()
	used := 0
	for _, svc := range cfg.ServicesOfType("sonarr") {
		state := states[svc.ID]
		if state == nil || len(state.Wanted) == 0 {
			continue
		}
		state = stateFor(states, svc.ID)
		client, err := driver.inv.SonarrClient(svc.ID)
		if err != nil {
			return used, err
		}
		client = client.WithContext(ctx)
		queued, err := client.QueuedEpisodes()
		if err != nil {
			return used, err
		}
		// Downloads started: imported, or back in line if they stopped.
		for id, want := range state.Wanted {
			if !want.Grabbed && queued[id] {
				// The RSS got it before a search did.
				want.Grabbed = true
				want.Result = "Downloading"
				state.Wanted[id] = want
				continue
			}
			if !want.Grabbed || queued[id] {
				continue
			}
			episode, err := client.GetEpisode(id)
			switch {
			case arr.IsNotFound(err):
				delete(state.Wanted, id)
			case err != nil:
				continue
			case episode.HasFile:
				delete(state.Wanted, id)
			case driver.autoImportOff[svc.ID] && state.importing(want.SeriesID):
				// Overtide's own import, not settled yet.
			default:
				want.Grabbed = false
				want.Tries++
				want.NextTry = driver.wantRetry(want)
				want.Result = "The download stopped before it was imported"
				state.Wanted[id] = want
			}
		}
		if dryRun {
			continue
		}
		episodesOf := map[int][]sonarr.Episode{}
		var due []int
		for id, want := range state.Wanted {
			// What older versions saved lacks these.
			if want.AiredAt.IsZero() || want.Episode == 0 {
				list, known := episodesOf[want.SeriesID]
				if !known {
					if list, err = client.Episodes(want.SeriesID); err != nil {
						continue
					}
					episodesOf[want.SeriesID] = list
				}
				if i := slices.IndexFunc(list, func(e sonarr.Episode) bool { return e.ID == id }); i >= 0 {
					want.AiredAt, want.Episode = list[i].AirDateUTC, list[i].EpisodeNumber
					state.Wanted[id] = want
				}
			}
			if budget <= 0 || want.Grabbed || want.NextTry.After(now) {
				continue
			}
			list, known := episodesOf[want.SeriesID]
			if !known {
				if list, err = client.Episodes(want.SeriesID); err != nil {
					continue
				}
				episodesOf[want.SeriesID] = list
			}
			out := want.Season <= 0 || seasonOut(list, want.Season, now)
			recent := !want.AiredAt.IsZero() && now.Sub(want.AiredAt) < tries.ReleaseGrace
			if !pick(want, !out && recent) {
				continue
			}
			if out && want.Season > 0 && state.Seasons[seasonKey(want.SeriesID, want.Season)].wanted() {
				continue // its season comes whole
			}
			due = append(due, id)
		}
		// The ones never tried, then the longest waiting.
		sort.Slice(due, func(i, j int) bool {
			a, b := state.Wanted[due[i]], state.Wanted[due[j]]
			if !a.NextTry.Equal(b.NextTry) {
				return a.NextTry.Before(b.NextTry)
			}
			return due[i] > due[j]
		})
		full := driver.fullDisk(cfg, svc.ID)
		names := driver.seriesNames(svc.ID)
		for _, id := range due {
			if budget <= 0 || ctx.Err() != nil || driver.Stopped() {
				break
			}
			want := state.Wanted[id]
			if full(want.SeriesID) {
				continue
			}
			episode, err := client.GetEpisode(id)
			if arr.IsNotFound(err) || (err == nil && episode.HasFile) {
				delete(state.Wanted, id)
				continue
			}
			if err != nil {
				continue
			}
			if !driver.Ready() {
				break
			}
			budget--
			used++
			driver.searchWanted(ctx, client, state, id, names[want.SeriesID])
			// Saved after each search: the page shows it at once, and it
			// isn't lost if the run stops.
			if err := driver.SaveStates(states); err != nil {
				return used, err
			}
			if driver.Stopped() {
				break
			}
		}
	}
	return used, driver.SaveStates(states)
}

// searchWanted searches for one wanted episode, Sonarr choosing the
// release, and notes what came of it.
func (driver *Driver) searchWanted(ctx context.Context, client *sonarr.Client, state *State, id int, series string) {
	want := state.Wanted[id]
	now := driver.now()
	driver.Step(episodeName(series, want.Season, want.Episode), "Sonarr is searching its trackers")
	want.LastTry = now
	started := time.Now()
	grabbed, err := tries.SearchAndCheck(ctx,
		func() (int, error) { return client.SearchEpisodes([]int{id}) },
		client.CommandStatus,
		func() (bool, error) {
			queued, err := client.QueuedEpisodes()
			return queued[id], err
		})
	switch down, _ := driver.Unavailable(started); {
	case err == nil && grabbed:
		want.Grabbed = true
		want.Result = "Downloading"
	case down:
		want.NextTry = now.Add(tries.LaterOnError)
		want.Result = "Not searched in full: not every tracker answered"
	case err != nil:
		want.Tries++
		want.NextTry = driver.wantRetry(want)
		want.Result = err.Error()
	default:
		want.Tries++
		want.NextTry = driver.wantRetry(want)
		want.Result = fmt.Sprintf("Nothing found (try %d)", want.Tries)
	}
	state.Wanted[id] = want
}

// wantRetry is when an episode that didn't come is searched for next: every
// day until tries.ReleaseGrace after it aired, then later and later
// (tries.RetryDelay).
func (driver *Driver) wantRetry(want EpisodeWant) time.Time {
	now := driver.now()
	if !want.AiredAt.IsZero() && now.Sub(want.AiredAt) < tries.ReleaseGrace {
		return now.Add(24 * time.Hour)
	}
	return now.Add(tries.RetryDelay(want.Tries))
}
