package sonarrdriver

import (
	"math"
	"sort"
	"time"

	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/videolength"
)

// The length rule: a season's files, taken in order, are the episodes whose
// runtimes add up to their lengths, in the one order (Sonarr's, or one of
// TMDB's) where every file lines up. A file of two 11-minute segments is
// those two episodes; a release in another order is placed by that order.
// It settles what neither titles nor numbers can, and only when one order
// fits every file.

const (
	// maxPerFile is the most episodes one file may hold.
	maxPerFile = 3
	// lengthMargin is how far a file's length may be from its episodes'
	// runtimes (TMDB gives them in whole minutes, often rounded): the larger
	// of this share of the runtimes and minLengthMargin.
	lengthMargin    = 0.15
	minLengthMargin = 2.0
)

// lengthEpisode is an episode in one order, with its runtime in minutes.
type lengthEpisode struct {
	ID      int
	Minutes int
}

// lengthFits says whether a file of length minutes can be runtimes.
func lengthFits(minutes float64, runtimes int) bool {
	margin := math.Max(lengthMargin*float64(runtimes), minLengthMargin)
	return math.Abs(minutes-float64(runtimes)) <= margin
}

// fitLengths walks the files (their lengths in minutes, in order) along the
// episodes of one order, from its first: each file takes the run of one to
// maxPerFile episodes whose runtimes fit its length, and only one run may
// fit. False when a file fits none, or more than one, or an episode's
// runtime isn't known. Episodes left after the last file are fine: a pack
// may lack the season's end.
func fitLengths(lengths []float64, episodes []lengthEpisode) ([][]int, bool) {
	out := make([][]int, len(lengths))
	next := 0
	for i, minutes := range lengths {
		taken := 0
		for k := 1; k <= maxPerFile && next+k <= len(episodes); k++ {
			sum := 0
			for _, e := range episodes[next : next+k] {
				if e.Minutes <= 0 {
					return nil, false
				}
				sum += e.Minutes
			}
			if lengthFits(minutes, sum) {
				if taken != 0 {
					return nil, false // two runs fit: the length can't tell
				}
				taken = k
			}
		}
		if taken == 0 {
			return nil, false
		}
		for _, e := range episodes[next : next+taken] {
			out[i] = append(out[i], e.ID)
		}
		next += taken
	}
	return out, true
}

// fitOrders fits the files against each order; the answer stands only when
// the orders that fit all place the files alike.
func fitOrders(lengths []float64, orders [][]lengthEpisode) ([][]int, bool) {
	var found [][]int
	for _, episodes := range orders {
		got, ok := fitLengths(lengths, episodes)
		if !ok {
			continue
		}
		if found != nil && !samePlaces(found, got) {
			return nil, false
		}
		found = got
	}
	return found, found != nil
}

func samePlaces(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// seasonOrders are a season's episodes in each order a release may follow,
// as Sonarr's episode IDs with TMDB's runtimes: Sonarr's own order (each
// episode's runtime from the TMDB episode it is, tmdbEpisodes), then TMDB's
// other orders, each of their episodes found among Sonarr's by its title.
// An order with an episode it can't place is left out.
func seasonOrders(season int, episodes []sonarr.Episode, orders *overfin.EpisodeOrders, now time.Time) [][]lengthEpisode {
	if orders == nil {
		return nil
	}
	var out [][]lengthEpisode
	tmdb := tmdbEpisodes(episodes, orders, now)
	var own []sonarr.Episode
	for _, e := range episodes {
		if e.SeasonNumber == season {
			own = append(own, e)
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i].EpisodeNumber < own[j].EpisodeNumber })
	sonarrs := make([]lengthEpisode, 0, len(own))
	for _, e := range own {
		sonarrs = append(sonarrs, lengthEpisode{ID: e.ID, Minutes: tmdb[e.ID].Runtime})
	}
	if len(sonarrs) > 0 {
		out = append(out, sonarrs)
	}
	titles := titlesOf(episodes, orders, now)
	runtimeOf := map[string]int{}
	for _, s := range orders.Seasons {
		for _, e := range s.Episodes {
			if e.Runtime > 0 {
				runtimeOf[comparableTitle(e.Name)] = e.Runtime
			}
		}
	}
	for _, other := range orders.Others {
		for _, s := range other.Seasons {
			if s.Season != season {
				continue
			}
			seq := make([]lengthEpisode, 0, len(s.Episodes))
			for _, e := range s.Episodes {
				ids := titles[comparableTitle(e.Name)]
				if len(ids) != 1 {
					seq = nil
					break
				}
				minutes := e.Runtime
				if minutes == 0 {
					minutes = runtimeOf[comparableTitle(e.Name)]
				}
				seq = append(seq, lengthEpisode{ID: ids[0], Minutes: minutes})
			}
			if len(seq) > 0 {
				out = append(out, seq)
			}
		}
	}
	return out
}

// fileLengths reads each file's length in minutes from its headers; a file
// it can't read is left out.
func fileLengths(paths []string) map[string]float64 {
	out := map[string]float64{}
	for _, p := range paths {
		if d, err := videolength.Of(p); err == nil {
			out[p] = d.Minutes()
		}
	}
	return out
}

// placeByLength places a season's files by the length rule: the files in
// the order their names number them, every one's length known. False when
// it doesn't settle them.
func placeByLength(season int, files []string, lengths map[string]float64, episodes []sonarr.Episode, orders *overfin.EpisodeOrders, now time.Time) (map[string][]int, bool) {
	if len(files) == 0 {
		return nil, false
	}
	sorted := append([]string(nil), files...)
	sort.SliceStable(sorted, func(i, j int) bool {
		_, a, _ := fileNumber(sorted[i])
		_, b, _ := fileNumber(sorted[j])
		if a != b {
			return a < b
		}
		return sorted[i] < sorted[j]
	})
	minutes := make([]float64, 0, len(sorted))
	for _, f := range sorted {
		m, ok := lengths[f]
		if !ok {
			return nil, false
		}
		minutes = append(minutes, m)
	}
	places, ok := fitOrders(minutes, seasonOrders(season, episodes, orders, now))
	if !ok {
		return nil, false
	}
	out := make(map[string][]int, len(sorted))
	for i, f := range sorted {
		out[f] = places[i]
	}
	return out, true
}
