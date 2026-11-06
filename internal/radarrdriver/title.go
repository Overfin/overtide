// Package radarrdriver is how Overtide gets films out of Radarr: looking
// one up, adding it, monitoring it and searching for it. What to fetch and
// when is the adding side's (internal/adding).
package radarrdriver

import (
	"context"
	"time"

	"github.com/overfin/overtide/internal/services/arr"
	"github.com/overfin/overtide/internal/services/radarr"
	"github.com/overfin/overtide/internal/tries"
)

// Title is what a try needs from Radarr for the film with this TMDB ID.
func Title(client *radarr.Client, tmdbID int, now func() time.Time) tries.Title {
	var lookup radarr.MovieLookup
	return tries.Title{
		Name: "Radarr",
		Lookup: func() (bool, bool, time.Time, error) {
			var err error
			lookup, err = client.LookupMovie(tmdbID)
			if arr.IsNotFound(err) {
				return true, false, time.Time{}, nil
			}
			released, at := Released(lookup, now())
			return false, released, at, err
		},
		Destination: client.Destination,
		Add: func(root string, profile, tag int) (int, error) {
			return client.AddMovie(lookup, root, profile, tag)
		},
		Tag:       client.TagMovie,
		EnsureTag: client.EnsureTag,
		Monitor:   client.SetMonitored,
		Search: func(ctx context.Context, id int) (bool, error) {
			started := now()
			return tries.SearchAndCheck(ctx,
				func() (int, error) { return client.SearchMovie(id) },
				client.CommandStatus,
				func() (bool, error) { return client.GrabbedSince(id, started) })
		},
		Grabbed:  client.GrabbedSince,
		HasFiles: client.MovieHasFile,
		Queued:   client.Queued,
		Gone:     arr.IsNotFound,
	}
}

// Released says whether a film is out at home (digital or disc), and if
// not, when to try it: its release, or 90 days after cinemas when that's
// all that's known, or in a week when nothing is.
func Released(lookup radarr.MovieLookup, now time.Time) (bool, time.Time) {
	var home *time.Time
	for _, date := range []*time.Time{lookup.DigitalRelease, lookup.PhysicalRelease} {
		if date != nil && (home == nil || date.Before(*home)) {
			home = date
		}
	}
	switch {
	case home != nil && !home.After(now):
		return true, time.Time{}
	case home != nil:
		return false, *home
	case lookup.Status == "released":
		return true, time.Time{}
	case lookup.InCinemas != nil && !lookup.InCinemas.AddDate(0, 0, 90).After(now):
		return true, time.Time{}
	case lookup.InCinemas != nil:
		return false, lookup.InCinemas.AddDate(0, 0, 90)
	}
	return false, now.Add(7 * 24 * time.Hour)
}
