package adding

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/radarr"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// pollEvery is how often a search command is checked while it runs.
var pollEvery = 5 * time.Second

const (
	searchTimeout = 10 * time.Minute
	// laterOnError is when a try that hit an error is made again.
	laterOnError = 6 * time.Hour
	// dryRunAgain spaces dry-run tries, which only read.
	dryRunAgain = 24 * time.Hour
)

// arr is what a try needs from Radarr or Sonarr for one title. For a
// series, monitoring and searching are the entry's seasons'.
type arr struct {
	name string
	// lookup reads the title before anything else: unknown when the
	// service has no record of it, released false with when it comes out.
	lookup func() (unknown bool, released bool, at time.Time, err error)
	// destination is the root folder and quality profile.
	destination func() (string, int, error)
	// add puts the title in, unmonitored and tagged; tag tags it.
	add       func(rootFolder string, profileID, tagID int) (int, error)
	tag       func(id, tagID int) error
	ensureTag func(label string) (int, error)
	monitor   func(id int, on bool) error
	// search asks for the search and says whether a download started.
	search   func(ctx context.Context, id int) (bool, error)
	grabbed  func(id int, since time.Time) (bool, error)
	hasFiles func(id int) (bool, error)
	// queued says whether a download of it is in the queue.
	queued func(id int) (bool, error)
	gone   func(error) bool
}

func (service *Service) arrFor(ctx context.Context, entry *Entry) (arr, error) {
	if entry.Kind == model.Series {
		client, err := service.inv.SonarrClient(entry.ServiceID)
		if err != nil {
			return arr{}, err
		}
		client = client.WithContext(ctx)
		var lookup sonarr.SeriesLookup
		seasons := entry.Seasons
		return arr{
			name: "Sonarr",
			lookup: func() (bool, bool, time.Time, error) {
				var err error
				lookup, err = client.LookupSeries(entry.ExternalID)
				if sonarr.IsNotFound(err) {
					return true, false, time.Time{}, nil
				}
				released, at := seriesReleased(lookup, service.now())
				return false, released, at, err
			},
			destination: client.Destination,
			add: func(root string, profile, tag int) (int, error) {
				return client.AddSeries(lookup, root, profile, tag)
			},
			tag:       client.TagSeries,
			ensureTag: client.EnsureTag,
			monitor: func(id int, on bool) error {
				return client.SetSeasonsMonitored(id, seasons, on)
			},
			search: func(ctx context.Context, id int) (bool, error) {
				started := service.now()
				for _, season := range seasons {
					if _, err := searchAndCheck(ctx,
						func() (int, error) { return client.SearchSeason(id, season) },
						client.CommandStatus,
						func() (bool, error) { return false, nil }); err != nil {
						return false, err
					}
				}
				return client.GrabbedSince(id, started)
			},
			grabbed:  client.GrabbedSince,
			hasFiles: func(id int) (bool, error) { return client.SeasonsHaveFiles(id, seasons) },
			queued:   client.Queued,
			gone:     sonarr.IsNotFound,
		}, nil
	}
	client, err := service.inv.RadarrClient(entry.ServiceID)
	if err != nil {
		return arr{}, err
	}
	client = client.WithContext(ctx)
	var lookup radarr.MovieLookup
	return arr{
		name: "Radarr",
		lookup: func() (bool, bool, time.Time, error) {
			var err error
			lookup, err = client.LookupMovie(entry.ExternalID)
			if radarr.IsNotFound(err) {
				return true, false, time.Time{}, nil
			}
			released, at := movieReleased(lookup, service.now())
			return false, released, at, err
		},
		destination: client.Destination,
		add: func(root string, profile, tag int) (int, error) {
			return client.AddMovie(lookup, root, profile, tag)
		},
		tag:       client.TagMovie,
		ensureTag: client.EnsureTag,
		monitor:   client.SetMonitored,
		search: func(ctx context.Context, id int) (bool, error) {
			started := service.now()
			return searchAndCheck(ctx,
				func() (int, error) { return client.SearchMovie(id) },
				client.CommandStatus,
				func() (bool, error) { return client.GrabbedSince(id, started) })
		},
		grabbed:  client.GrabbedSince,
		hasFiles: client.MovieHasFile,
		queued:   client.Queued,
		gone:     radarr.IsNotFound,
	}, nil
}

// try tries one title. Out more than ReleaseWindow from now, it waits,
// unmonitored. Otherwise it goes in Radarr or Sonarr (added once,
// unmonitored and tagged, if it isn't there) and is monitored: about to
// come out, without a search, so their RSS catches the release (follow
// looks for the grab); out, with a search of that title, and if nothing
// started it's unmonitored again until its next try. In dry run, only the
// reading is done. It returns how many searches it asked for (none in dry
// run), what the run's search budget counts.
func (service *Service) try(ctx context.Context, cfg config.Config, entry *Entry, dryRun bool) int {
	now := service.now()
	if entry.Kind == model.Series && len(entry.Seasons) == 0 {
		// Saved by an older version: the seasons come with Overfin's next read.
		service.later(entry, "Waiting for the seasons Overfin suggests")
		return 0
	}
	a, err := service.arrFor(ctx, entry)
	if err != nil {
		service.later(entry, err.Error())
		return 0
	}
	unknown, released, at, err := a.lookup()
	switch {
	case err != nil:
		service.later(entry, "Couldn't look it up: "+err.Error())
		return 0
	case unknown:
		service.failed(entry, fmt.Sprintf("%s doesn't know this title (ID %d)", a.name, entry.ExternalID))
		return 0
	case !released && at.Sub(now) > ReleaseWindow:
		entry.State, entry.NextTry = NotReleased, at.Add(-ReleaseWindow)
		entry.Result = "Not out yet (" + at.Format("2006-01-02") + "); monitored two days before"
		return 0
	}
	rootFolder, profileID, err := a.destination()
	if err != nil {
		service.later(entry, err.Error())
		return 0
	}
	disk := service.diskStop
	if service.disk != nil {
		disk = service.disk
	}
	if stop := disk(cfg, rootFolder, dryRun); stop != "" {
		service.later(entry, stop)
		return 0
	}
	if dryRun {
		entry.NextTry = now.Add(dryRunAgain)
		what := "monitor it and search for it"
		if !released {
			what = "monitor it for its release on " + at.Format("2006-01-02")
		}
		if entry.ArrID == 0 {
			what = "add it to " + a.name + ", " + what
		}
		entry.Result = "Dry run: would " + what
		return 0
	}
	if label := tagFor(entry.Source); label != "" {
		tagID, err := a.ensureTag(label)
		if err != nil {
			service.later(entry, "Couldn't make the tag: "+err.Error())
			return 0
		}
		if entry.ArrID == 0 {
			id, err := a.add(rootFolder, profileID, tagID)
			if err != nil {
				service.later(entry, "Couldn't add it: "+err.Error())
				return 0
			}
			entry.ArrID = id
		} else if err := a.tag(entry.ArrID, tagID); err != nil {
			service.later(entry, "Couldn't tag it: "+err.Error())
			return 0
		}
	}
	if err := a.monitor(entry.ArrID, true); err != nil {
		service.later(entry, "Couldn't monitor it: "+err.Error())
		return 0
	}
	if !released {
		entry.State, entry.ReleaseAt, entry.MonitoredAt = NotReleased, at, now
		entry.NextTry = at.Add(ReleaseGrace)
		entry.Result = "Monitored for its release on " + at.Format("2006-01-02")
		return 0
	}
	searches := entry.searches()
	started := time.Now()
	grabbed, err := a.search(ctx, entry.ArrID)
	if err == nil && grabbed {
		service.downloading(entry, a, false)
		return searches
	}
	if unmonitorErr := a.monitor(entry.ArrID, false); unmonitorErr != nil {
		service.later(entry, "Nothing started, and couldn't unmonitor it: "+unmonitorErr.Error())
		return searches
	}
	if err != nil {
		service.later(entry, "Unmonitored: "+err.Error())
		return searches
	}
	if down, why := service.unavailable(started); down {
		service.later(entry, "Not searched in full ("+why+"); tried again later")
		return searches
	}
	service.failed(entry, "Nothing found")
	return searches
}

// searchAndCheck asks for the search, waits for it to finish, then says
// whether it started a download.
func searchAndCheck(ctx context.Context, search func() (int, error), status func(int) (string, error), grabbed func() (bool, error)) (bool, error) {
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
		case <-time.After(pollEvery):
		}
	}
	return grabbed()
}

// failed counts a try that found nothing (or a title Radarr or Sonarr has
// no record of, often one too new for their sources): it's tried again
// after retryDelay, unless the lifecycle takes it out first.
// downloading marks a download confirmed and unmonitors the title, so
// Radarr and Sonarr don't upgrade it on their own: Overtide decides what
// comes in. It's imported all the same (neither checks monitoring to
// import). If unmonitoring fails, follow tries again.
func (service *Service) downloading(entry *Entry, a arr, dryRun bool) {
	entry.State, entry.Result = Downloading, "Downloading"
	entry.Unmonitored = !dryRun && a.monitor(entry.ArrID, false) == nil
}

func (service *Service) failed(entry *Entry, why string) {
	entry.State = Waiting
	entry.Tries++
	entry.NextTry = service.now().Add(retryDelay(entry.Tries))
	entry.Result = fmt.Sprintf("%s (try %d)", why, entry.Tries)
}

func (service *Service) later(entry *Entry, why string) {
	entry.NextTry, entry.Result = service.now().Add(laterOnError), why
}

// diskStop says why nothing may be added to the root folder's disk now, or
// "". The usage target is what removal steers toward, not a stop: adding
// goes on past it and removal makes room after. Only the critical threshold
// stops adding, until removal catches up, so downloads can't fill the disk.
// A disk that can't be read stops it too (in dry run, which adds nothing,
// one not known yet is let through).
func (service *Service) diskStop(cfg config.Config, rootFolder string, dryRun bool) string {
	root := filepath.Clean(rootFolder)
	for _, device := range service.inv.StorageDevices() {
		for _, paths := range device.ServiceRoots {
			for _, path := range paths {
				path = filepath.Clean(path)
				if root != path && !strings.HasPrefix(root, path+string(filepath.Separator)) && !strings.HasPrefix(path, root+string(filepath.Separator)) {
					continue
				}
				usable := device.UsableBytes()
				if !device.Available || usable == 0 {
					return "Its disk can't be read now"
				}
				_, critical := cfg.ThresholdsFor(device.RepresentativePath)
				if used := float64(device.OvertideUsedBytes()) / float64(usable) * 100; used >= critical {
					return fmt.Sprintf("Its disk is past its critical threshold (%.1f%% used, critical %.1f%%)", used, critical)
				}
				return ""
			}
		}
	}
	if dryRun {
		return ""
	}
	return "Its disk isn't known yet (" + rootFolder + ")"
}

// movieReleased says whether a film is out at home (digital or disc), and
// if not, when to try it: its release, or 90 days after cinemas when
// that's all that's known, or in a week when nothing is.
func movieReleased(lookup radarr.MovieLookup, now time.Time) (bool, time.Time) {
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

// seriesReleased says whether a series has started airing, and if not,
// when to try it: its first air date, or in a week when it's unknown.
func seriesReleased(lookup sonarr.SeriesLookup, now time.Time) (bool, time.Time) {
	switch {
	case lookup.FirstAired != nil && lookup.FirstAired.After(now):
		return false, *lookup.FirstAired
	case lookup.Status == "upcoming":
		return false, now.Add(7 * 24 * time.Hour)
	}
	return true, time.Time{}
}
