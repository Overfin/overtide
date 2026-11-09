package sonarrdriver

import (
	"context"
	"time"

	"github.com/overfin/overtide/internal/services/arr"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/tries"
)

// Title is what a try needs from Sonarr for the series with this TheTVDB
// ID: monitoring and searching are these seasons'.
func Title(client *sonarr.Client, tvdbID int, seasons []int, now func() time.Time) tries.Title {
	var lookup sonarr.SeriesLookup
	return tries.Title{
		Name: "Sonarr",
		Lookup: func() (bool, bool, time.Time, error) {
			var err error
			lookup, err = client.LookupSeries(tvdbID)
			if arr.IsNotFound(err) {
				return true, false, time.Time{}, nil
			}
			released, at := Released(lookup, now())
			return false, released, at, err
		},
		Destination: client.Destination,
		Add: func(root string, profile, tag int) (int, error) {
			id, err := client.AddSeries(lookup, root, profile, tag)
			if err == nil {
				client.WaitAdded(id)
			}
			return id, err
		},
		Tag:       client.TagSeries,
		EnsureTag: client.EnsureTag,
		Monitor: func(id int, on bool) error {
			return client.SetSeasonsMonitored(id, seasons, on)
		},
		Search: func(ctx context.Context, id int) (bool, error) {
			started := now()
			for _, season := range seasons {
				if _, err := tries.SearchAndCheck(ctx,
					func() (int, error) { return client.SearchSeason(id, season) },
					client.CommandStatus,
					func() (bool, error) { return false, nil }); err != nil {
					return false, err
				}
			}
			return client.GrabbedSince(id, started)
		},
		Grabbed:  client.GrabbedSince,
		HasFiles: func(id int) (bool, error) { return client.SeasonsHaveFiles(id, seasons) },
		Queued:   client.Queued,
		Gone:     arr.IsNotFound,
	}
}

// Released says whether a series has started airing, and if not, when to
// try it: its first air date, or in a week when it's unknown.
func Released(lookup sonarr.SeriesLookup, now time.Time) (bool, time.Time) {
	switch {
	case lookup.FirstAired != nil && lookup.FirstAired.After(now):
		return false, *lookup.FirstAired
	case lookup.Status == "upcoming":
		return false, now.Add(7 * 24 * time.Hour)
	}
	return true, time.Time{}
}
