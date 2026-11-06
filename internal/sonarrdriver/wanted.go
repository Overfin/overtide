package sonarrdriver

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/tries"
)

// WantedRow is an episode wanted, or a season waiting for its whole-season
// search, for the Adding page.
type WantedRow struct {
	Title string
	// Why is what puts it there, in words.
	Why string
	// When is its next search; zero for the next run's, or one that isn't
	// searched for now (Result says why).
	When   time.Time
	Result string
}

// Wanted lists the episodes wanted and the seasons waiting for a
// whole-season search, the next to be searched first. It reads what was last
// saved: a run saves it after each search, so it doesn't wait for the run.
func (driver *Driver) Wanted() (episodes, seasons []WantedRow, err error) {
	states, err := driver.States()
	if err != nil {
		return nil, nil, err
	}
	now := driver.now()
	for serviceID, state := range states {
		names := driver.seriesNames(serviceID)
		for _, pack := range state.Seasons {
			if pack.Done && !pack.short() {
				continue
			}
			row := WantedRow{Title: seasonName(names[pack.SeriesID], pack.Season), Why: "Whole season"}
			switch {
			case len(pack.Unmatched) > 0:
				row.Why = "Needs a look"
				row.Result = fmt.Sprintf("Its download has files that match none of Sonarr's episodes (%s): the numbering may differ. Left in the queue, and not searched again.", strings.Join(pack.Unmatched, ", "))
			case pack.short():
				row.Why = "Needs a look"
				row.Result = fmt.Sprintf("A whole-season pack came, and %d episodes are still missing: its files may not match Sonarr's episodes. Not searched again.", pack.Short)
			case !pack.GrabbedAt.IsZero():
				row.Result = "Downloading: " + pack.Release
			case pack.Tries >= tries.Max:
				row.Result = fmt.Sprintf("No whole season in %d tries: its episodes are searched one by one", pack.Tries)
			case pack.Tries > 0:
				row.When, row.Result = pack.NextTry, fmt.Sprintf("No whole season found (try %d)", pack.Tries)
			default:
				row.When, row.Result = pack.NextTry, "Not searched yet"
			}
			seasons = append(seasons, row)
		}
		for _, want := range state.Wanted {
			row := WantedRow{Title: episodeName(names[want.SeriesID], want.Season, want.Episode), Why: "Missing", When: want.NextTry, Result: want.Result}
			if !want.AiredAt.IsZero() && now.Sub(want.AiredAt) < tries.ReleaseGrace {
				row.Why = "Just aired"
			}
			switch {
			case want.Grabbed:
				row.When = time.Time{}
			case want.Season > 0 && state.Seasons[seasonKey(want.SeriesID, want.Season)].pending():
				row.When, row.Result = time.Time{}, "Waits for its whole season"
			case row.Result == "":
				row.Result = "Not searched yet"
			}
			episodes = append(episodes, row)
		}
	}
	byWhen := func(list []WantedRow) {
		sort.SliceStable(list, func(i, j int) bool {
			if !list[i].When.Equal(list[j].When) {
				return list[i].When.Before(list[j].When)
			}
			return list[i].Title < list[j].Title
		})
	}
	byWhen(episodes)
	byWhen(seasons)
	return episodes, seasons, nil
}
