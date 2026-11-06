package sonarrdriver

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/redirect"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/torrent"
	"github.com/overfin/overtide/internal/tries"
)

const (
	// maxRead is how many releases of a search have their file lists read.
	maxRead = 5
	// readPause is the wait between two reads: trackers ask for few requests
	// at a time. readRetry is the wait before a read's second chance.
	readPause = 2 * time.Second
	readRetry = 6 * time.Second
	// maxTorrent bounds a .torrent's size.
	maxTorrent = 20 << 20
	// titledEnough is the part of a release's files whose titles must be
	// known for it to count as titled.
	titledEnough = 0.7
)

var videoExtensions = []string{".mkv", ".mp4", ".avi", ".m4v", ".ts", ".wmv"}

// torrentClient reads .torrents. Its address comes from Sonarr, and may hold
// a tracker's key: an error never says it.
var torrentClient = &http.Client{Timeout: 40 * time.Second, CheckRedirect: redirect.Check}

// readTorrent reads the list of files of a release, one chance to retry. Its
// errors don't name the address.
func readTorrent(ctx context.Context, address string) ([]torrent.File, error) {
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("no .torrent to read")
	}
	var files []torrent.File
	for attempt := 0; ; attempt++ {
		files, err = fetchTorrent(ctx, address)
		if err == nil || attempt > 0 {
			return files, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(readWait(readRetry)):
		}
	}
}

// readWait is a pause, none in tests.
func readWait(wait time.Duration) time.Duration {
	if tries.PollEvery == time.Millisecond {
		return time.Millisecond
	}
	return wait
}

func fetchTorrent(ctx context.Context, address string) ([]torrent.File, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("no .torrent to read")
	}
	response, err := torrentClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("the .torrent couldn't be fetched")
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("the .torrent answered %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxTorrent+1))
	if err != nil || len(data) > maxTorrent {
		return nil, fmt.Errorf("the .torrent couldn't be read")
	}
	return torrent.Files(data)
}

// releaseFit says how well a release's files suit a season.
type releaseFit struct {
	// fits: as many videos as the season has episodes. titled: most of the
	// videos' names give a title Sonarr or TMDB knows.
	fits, titled bool
}

// fitOf reads a release's list of files. Samples don't count.
func fitOf(files []torrent.File, season int, episodes []sonarr.Episode, index titleIndex) releaseFit {
	videos, known, inSeason := 0, 0, 0
	for _, e := range episodes {
		if e.SeasonNumber == season {
			inSeason++
		}
	}
	for _, file := range files {
		name := strings.ToLower(path.Base(file.Path))
		if !slices.Contains(videoExtensions, path.Ext(name)) || strings.Contains(name, "sample") {
			continue
		}
		videos++
		if len(index[comparableTitle(fileTitle(file.Path))]) == 1 {
			known++
		}
	}
	return releaseFit{fits: videos > 0 && videos == inSeason, titled: videos > 0 && float64(known) >= titledEnough*float64(videos)}
}

// rank orders what suits a season, the lowest first: both, then what would
// go in by itself. Where a season's numbers can be believed, a fitting pack
// goes in as it is, and a titled one comes next; otherwise only titles say
// anything, so those come first.
func (fit releaseFit) rank(numbersHold bool) int {
	switch {
	case fit.fits && fit.titled:
		return 0
	case fit.fits && numbersHold, fit.titled && !numbersHold:
		return 1
	case fit.fits, fit.titled:
		return 2
	}
	return 3
}

// chooseRelease picks which release of a search to grab, when Overtide does
// the imports: reading what's inside the likely ones, one at a time, it takes
// what would go in by itself, and always something. Releases tried already
// come last. Nothing can be read: Sonarr's order stands.
func (driver *Driver) chooseRelease(ctx context.Context, releases []sonarr.Release, pack *SeasonPack, episodes []sonarr.Episode, orders *overfin.EpisodeOrders) *sonarr.Release {
	var candidates []*sonarr.Release
	var tried []*sonarr.Release
	for i := range releases {
		if !acceptablePack(releases[i], pack.Season) {
			continue
		}
		if slices.Contains(pack.Tried, releases[i].Title) {
			tried = append(tried, &releases[i])
		} else {
			candidates = append(candidates, &releases[i])
		}
	}
	if len(candidates) == 0 {
		if len(tried) > 0 {
			return tried[0]
		}
		return nil
	}
	now := driver.now()
	numbersHold := seasonCheck(pack.Season, episodes, orders, now) == ""
	index := titlesOf(episodes, orders, now)
	best, bestRank := candidates[0], 4
	for i, release := range candidates {
		if i >= maxRead || bestRank == 0 || ctx.Err() != nil {
			break
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				return best
			case <-time.After(readWait(readPause)):
			}
		}
		files, err := readTorrent(ctx, release.DownloadURL)
		if err != nil {
			log.Printf("[adding] reading what's in %s: %v", release.Title, err)
			continue
		}
		if rank := fitOf(files, pack.Season, episodes, index).rank(numbersHold); rank < bestRank {
			best, bestRank = release, rank
		}
	}
	return best
}
