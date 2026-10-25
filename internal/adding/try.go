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

// try tries one title: not out yet, it waits for its release; otherwise
// it's added (unless it's already there), searched for, and kept if a
// download started, else taken out again to be retried later. In dry run,
// only the reading is done.
func (service *Service) try(ctx context.Context, cfg config.Config, entry *Entry, dryRun bool) {
	if entry.Kind == model.Series {
		service.trySeries(ctx, cfg, entry, dryRun)
		return
	}
	service.tryMovie(ctx, cfg, entry, dryRun)
}

func (service *Service) tryMovie(ctx context.Context, cfg config.Config, entry *Entry, dryRun bool) {
	now := service.now()
	client, err := service.inv.RadarrClient(entry.ServiceID)
	if err != nil {
		service.later(entry, err.Error())
		return
	}
	client = client.WithContext(ctx)
	lookup, err := client.LookupMovie(entry.ExternalID)
	if radarr.IsNotFound(err) {
		service.unknown(entry, fmt.Sprintf("Radarr doesn't know this film (TMDB ID %d)", entry.ExternalID))
		return
	}
	if err != nil {
		service.later(entry, "Couldn't look it up: "+err.Error())
		return
	}
	if released, at := movieReleased(lookup, now); !released {
		service.notReleased(entry, at)
		return
	}
	var rootFolder string
	var profileID int
	if entry.ArrID == 0 {
		if rootFolder, profileID, err = client.Destination(); err != nil {
			service.later(entry, err.Error())
			return
		}
		if stop := service.diskStop(cfg, rootFolder, dryRun); stop != "" {
			service.later(entry, stop)
			return
		}
	}
	if dryRun {
		entry.State, entry.NextTry = Waiting, now.Add(dryRunAgain)
		entry.Result = "Dry run: would add it to Radarr and search for it"
		if entry.ArrID != 0 {
			entry.Result = "Dry run: would tag it and search for it"
		}
		return
	}
	tagID, err := client.EnsureTag(Tag)
	if err != nil {
		service.later(entry, "Couldn't make the tag: "+err.Error())
		return
	}
	if entry.ArrID == 0 {
		// Released: tries wait for the release already (movieReleased).
		id, err := client.AddMovie(lookup, rootFolder, profileID, "released", tagID)
		if err != nil {
			service.later(entry, "Couldn't add it: "+err.Error())
			return
		}
		entry.ArrID, entry.Ours = id, true
	} else if err := client.TagMovie(entry.ArrID, tagID); err != nil {
		service.later(entry, "Couldn't tag it: "+err.Error())
		return
	}
	started := service.now()
	grabbed, err := searchAndCheck(ctx,
		func() (int, error) { return client.SearchMovie(entry.ArrID) },
		client.CommandStatus,
		func() (bool, error) { return client.GrabbedSince(entry.ArrID, started) })
	service.settle(entry, grabbed, err, func() error { return client.DeleteMovie(entry.ArrID) })
}

func (service *Service) trySeries(ctx context.Context, cfg config.Config, entry *Entry, dryRun bool) {
	now := service.now()
	client, err := service.inv.SonarrClient(entry.ServiceID)
	if err != nil {
		service.later(entry, err.Error())
		return
	}
	client = client.WithContext(ctx)
	lookup, err := client.LookupSeries(entry.ExternalID)
	if sonarr.IsNotFound(err) {
		service.unknown(entry, fmt.Sprintf("Sonarr doesn't know this series (TVDB ID %d)", entry.ExternalID))
		return
	}
	if err != nil {
		service.later(entry, "Couldn't look it up: "+err.Error())
		return
	}
	if released, at := seriesReleased(lookup, now); !released {
		service.notReleased(entry, at)
		return
	}
	var rootFolder string
	var profileID int
	if entry.ArrID == 0 {
		if rootFolder, profileID, err = client.Destination(); err != nil {
			service.later(entry, err.Error())
			return
		}
		if stop := service.diskStop(cfg, rootFolder, dryRun); stop != "" {
			service.later(entry, stop)
			return
		}
	}
	if dryRun {
		entry.State, entry.NextTry = Waiting, now.Add(dryRunAgain)
		entry.Result = "Dry run: would add it to Sonarr and search for it"
		if entry.ArrID != 0 {
			entry.Result = "Dry run: would tag it and search for it"
		}
		return
	}
	tagID, err := client.EnsureTag(Tag)
	if err != nil {
		service.later(entry, "Couldn't make the tag: "+err.Error())
		return
	}
	if entry.ArrID == 0 {
		id, err := client.AddSeries(lookup, rootFolder, profileID, tagID)
		if err != nil {
			service.later(entry, "Couldn't add it: "+err.Error())
			return
		}
		entry.ArrID, entry.Ours = id, true
	} else if err := client.TagSeries(entry.ArrID, tagID); err != nil {
		service.later(entry, "Couldn't tag it: "+err.Error())
		return
	}
	started := service.now()
	grabbed, err := searchAndCheck(ctx,
		func() (int, error) { return client.SearchSeries(entry.ArrID) },
		client.CommandStatus,
		func() (bool, error) { return client.GrabbedSince(entry.ArrID, started) })
	service.settle(entry, grabbed, err, func() error { return client.DeleteSeries(entry.ArrID) })
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

// settle ends a live try: a download started, the title is downloading;
// none, it's taken out of Radarr or Sonarr to be tried again later. A
// search that went wrong takes it out too, and it's tried again sooner.
func (service *Service) settle(entry *Entry, grabbed bool, searchErr error, remove func() error) {
	now := service.now()
	if searchErr == nil && grabbed {
		entry.State, entry.Result = Downloading, "Downloading"
		return
	}
	if err := remove(); err != nil {
		// Still there: the next run tries it again as a title it has.
		service.later(entry, "Nothing started, and couldn't take it out: "+err.Error())
		return
	}
	entry.ArrID, entry.State = 0, Waiting
	if searchErr != nil {
		entry.NextTry = now.Add(laterOnError)
		entry.Result = "Taken out: " + searchErr.Error()
		return
	}
	entry.Tries++
	entry.NextTry = now.Add(retryDelay(entry.Tries))
	entry.Result = fmt.Sprintf("Nothing found (try %d), taken out until the next try", entry.Tries)
}

// unknown is a title Radarr or Sonarr has no record of (often one too new
// for their sources): retried like one nothing was found for.
func (service *Service) unknown(entry *Entry, why string) {
	entry.Tries++
	entry.NextTry = service.now().Add(retryDelay(entry.Tries))
	entry.Result = fmt.Sprintf("%s; try %d, tried again later", why, entry.Tries)
}

func (service *Service) later(entry *Entry, why string) {
	entry.NextTry, entry.Result = service.now().Add(laterOnError), why
}

func (service *Service) notReleased(entry *Entry, at time.Time) {
	entry.State, entry.NextTry = NotReleased, at
	entry.Result = "Not out yet; tried once it is, " + at.Format("2006-01-02")
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
