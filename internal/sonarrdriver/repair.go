package sonarrdriver

import (
	"context"
	"log"
	"path"
	"slices"
	"strings"

	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/tries"
)

// torrentTwin is a library file's twin in a torrent: the same data (device
// and inode) at the torrent's path.
type torrentTwin struct {
	Path, Hash string
}

// torrentTwins finds each library file's twin in a torrent, from the file
// snapshot; a file without one is left out.
func (driver *Driver) torrentTwins(paths []string) map[string]torrentTwin {
	out := map[string]torrentTwin{}
	if driver.inv == nil {
		return out
	}
	files, _, torrentRefs, _, err := driver.inv.FileSnapshot()
	if err != nil {
		return out
	}
	type identity struct{ device, inode uint64 }
	byPath := map[string]identity{}
	for _, f := range files {
		if f.IdentityKnown {
			byPath[f.Path] = identity{f.Device, f.Inode}
		}
	}
	byIdentity := map[identity]torrentTwin{}
	for _, r := range torrentRefs {
		if id, ok := byPath[r.Path]; ok {
			byIdentity[id] = torrentTwin{Path: r.Path, Hash: strings.ToLower(r.Hash)}
		}
	}
	for _, p := range paths {
		if id, ok := byPath[p]; ok {
			if twin, ok := byIdentity[id]; ok {
				out[p] = twin
			}
		}
	}
	return out
}

// repairSeason puts right a season imported one episode a file when its
// files hold several (a release of 22-minute files where Sonarr counts
// 11-minute segments): the length rule on the files the library has, in
// the order their names number them, and when it shows files of several
// episodes, each file's torrent twin imported again with its episodes, in
// one command; Sonarr replaces the files and hardlinks the twins. several
// says the lengths show files of several episodes (repaired or not: a file
// without a torrent twin, and nothing is repaired). In dry run, nothing.
func (driver *Driver) repairSeason(ctx context.Context, client *sonarr.Client, series sonarr.Title, season int, episodes []sonarr.Episode, dryRun bool) (repaired, several bool) {
	records, err := client.EpisodeFiles(series.ID)
	if err != nil {
		return false, false
	}
	byFile := map[int]string{}
	for _, r := range records {
		if r.SeasonNumber == season {
			byFile[r.ID] = path.Join(series.Path, r.Relative)
		}
	}
	current := map[string][]int{}
	var paths []string
	for _, e := range episodes {
		p, ok := byFile[e.EpisodeFileID]
		if e.SeasonNumber != season || !ok {
			continue
		}
		if _, seen := current[p]; !seen {
			paths = append(paths, p)
		}
		current[p] = append(current[p], e.ID)
	}
	if len(paths) == 0 || !fromFirstWithoutGaps(paths, current, episodes) {
		// The length rule lays the files from the season's first episode
		// on, one after the other: with a file missing at the start or in
		// between (E06 never came), every file after it would be placed
		// on the wrong episodes.
		return false, false
	}
	orders := driver.episodeOrders(ctx, driver.inv.Config(), series.TMDBID)
	if orders == nil {
		return false, false
	}
	places, ok := placeByLength(season, paths, fileLengths(paths), episodes, orders, driver.now())
	if !ok {
		return false, false
	}
	differs := false
	for _, p := range paths {
		several = several || len(places[p]) > 1
		differs = differs || !slices.Equal(places[p], current[p])
	}
	if !several || !differs || dryRun {
		return false, several
	}
	find := driver.torrentTwins
	if driver.twinsOf != nil {
		find = driver.twinsOf
	}
	twins := find(paths)
	byFolder := map[string][]string{}
	for _, p := range paths {
		twin, ok := twins[p]
		if !ok {
			log.Printf("[adding] %s season %d: %s has no torrent to import again from", series.Title, season, path.Base(p))
			return false, true
		}
		byFolder[path.Dir(twin.Path)] = append(byFolder[path.Dir(twin.Path)], p)
	}
	// Asked by folder (Sonarr no longer lists a finished download's files),
	// each twin taken by its exact path: nothing else in the folder.
	var again []sonarr.ImportItem
	for folder, ps := range byFolder {
		items, err := client.FolderItems(folder)
		if err != nil {
			return false, true
		}
		for _, p := range ps {
			i := slices.IndexFunc(items, func(item sonarr.ImportItem) bool { return item.Path == twins[p].Path })
			if i < 0 {
				return false, true
			}
			item := items[i]
			item.Episodes = item.Episodes[:0:0]
			for _, id := range places[p] {
				item.Episodes = append(item.Episodes, sonarr.ImportEpisode{ID: id})
			}
			item.DownloadID = twins[p].Hash
			again = append(again, item)
		}
	}
	release := driver.inv.ShareFiles()
	_, err = tries.SearchAndCheck(ctx,
		func() (int, error) { return client.ImportAs(series.ID, again, "copy") },
		client.CommandStatus,
		func() (bool, error) { return true, nil })
	release()
	if err != nil {
		log.Printf("[adding] %s season %d, imported again: %v", series.Title, season, err)
		return false, true
	}
	log.Printf("[adding] %s season %d: %d files imported again, each with its episodes", series.Title, season, len(again))
	return true, true
}

// fromFirstWithoutGaps says whether the files, in order, hold the season's
// episodes from its first on (as Sonarr numbers them now), each file
// starting right after the one before.
func fromFirstWithoutGaps(paths []string, current map[string][]int, episodes []sonarr.Episode) bool {
	number := map[int]int{}
	for _, e := range episodes {
		number[e.ID] = e.EpisodeNumber
	}
	next := 1
	for _, p := range paths {
		var nums []int
		for _, id := range current[p] {
			nums = append(nums, number[id])
		}
		if len(nums) == 0 {
			return false
		}
		slices.Sort(nums)
		if nums[0] != next {
			return false
		}
		next = nums[len(nums)-1] + 1
	}
	return true
}
