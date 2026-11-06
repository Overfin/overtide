// Package tries is what the tries of titles, seasons and episodes share:
// how long they wait, and how a search in Radarr or Sonarr is followed.
package tries

import (
	"context"
	"fmt"
	"time"
)

// PollEvery is how often a search command is checked while it runs.
var PollEvery = 5 * time.Second

const (
	searchTimeout = 10 * time.Minute
	// LaterOnError is when a try that hit an error is made again.
	LaterOnError = 6 * time.Hour
)

// Fixed for now (see the README).
const (
	// Max: a suggestion leaves after that many tries that found nothing, and
	// a season stops waiting for its whole-season pack.
	Max = 5
	// Cooldown: a title that left the libraries, or the list unfinished,
	// isn't taken from Overfin's suggestions for that long.
	Cooldown = 183 * 24 * time.Hour
	// ReleaseWindow: a title coming out within it is monitored without a
	// search, so Radarr's or Sonarr's RSS catches the release, until
	// ReleaseGrace after the release.
	ReleaseWindow = 2 * 24 * time.Hour
	ReleaseGrace  = 14 * 24 * time.Hour
)

// RetryDelay is the wait after the tries-th try found nothing: a day,
// doubled each time, at most 30 days.
func RetryDelay(tries int) time.Duration {
	days := 1
	for i := 1; i < tries && days < 30; i++ {
		days *= 2
	}
	if days > 30 {
		days = 30
	}
	return time.Duration(days) * 24 * time.Hour
}

// SearchAndCheck asks for the search, waits for it to finish, then says
// whether it started a download.
func SearchAndCheck(ctx context.Context, search func() (int, error), status func(int) (string, error), grabbed func() (bool, error)) (bool, error) {
	commandID, err := search()
	if err != nil {
		return false, fmt.Errorf("couldn't ask for the search: %w", err)
	}
	deadline := time.Now().Add(searchTimeout)
	for {
		state, err := status(commandID)
		if err != nil {
			return false, fmt.Errorf("couldn't follow the search: %w", err)
		}
		if state == "completed" {
			break
		}
		if state == "failed" || state == "aborted" || state == "cancelled" {
			return false, fmt.Errorf("the search %s", state)
		}
		if time.Now().After(deadline) {
			return false, fmt.Errorf("the search took over %s", searchTimeout)
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(PollEvery):
		}
	}
	return grabbed()
}

// Title is what a try needs from Radarr or Sonarr for one title, as a
// driver gives it. For a series, monitoring and searching are the
// seasons' the driver was given.
type Title struct {
	Name string
	// Lookup reads the title before anything else: unknown when the
	// service has no record of it, released false with when it comes out.
	Lookup func() (unknown bool, released bool, at time.Time, err error)
	// Destination is the root folder and quality profile.
	Destination func() (string, int, error)
	// Add puts the title in, unmonitored and tagged; Tag tags it.
	Add       func(rootFolder string, profileID, tagID int) (int, error)
	Tag       func(id, tagID int) error
	EnsureTag func(label string) (int, error)
	Monitor   func(id int, on bool) error
	// Search asks for the search and says whether a download started.
	Search   func(ctx context.Context, id int) (bool, error)
	Grabbed  func(id int, since time.Time) (bool, error)
	HasFiles func(id int) (bool, error)
	// Queued says whether a download of it is in the queue.
	Queued func(id int) (bool, error)
	Gone   func(error) bool
}
