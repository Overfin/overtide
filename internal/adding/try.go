package adding

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/radarrdriver"
	"github.com/overfin/overtide/internal/sonarrdriver"
	"github.com/overfin/overtide/internal/tries"
)

// dryRunAgain spaces dry-run tries, which only read.
const dryRunAgain = 24 * time.Hour

// arrFor is what a try needs from Radarr or Sonarr for the entry, from
// their drivers.
func (service *Service) arrFor(ctx context.Context, entry *Entry) (tries.Title, error) {
	if entry.Kind == model.Series {
		client, err := service.inv.SonarrClient(entry.ServiceID)
		if err != nil {
			return tries.Title{}, err
		}
		return sonarrdriver.Title(client.WithContext(ctx), entry.ExternalID, entry.trySeasons(), service.now), nil
	}
	client, err := service.inv.RadarrClient(entry.ServiceID)
	if err != nil {
		return tries.Title{}, err
	}
	return radarrdriver.Title(client.WithContext(ctx), entry.ExternalID, service.now), nil
}

// try tries one title. Out more than tries.ReleaseWindow from now, it
// waits, unmonitored. Otherwise it goes in Radarr or Sonarr (added once,
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
	// Its destination, when the operator set one for it.
	serviceType := "radarr"
	if entry.Kind == model.Series {
		serviceType = "sonarr"
	}
	if d, ok := cfg.DestinationFor(serviceType, entry.Categories); ok && d.ServiceID == entry.ServiceID {
		a.Destination = func() (string, int, error) { return d.RootFolder, d.QualityProfileID, nil }
	}
	service.step(entry.Title, "Looking it up in "+a.Name)
	unknown, released, at, err := a.Lookup()
	switch {
	case err != nil:
		service.later(entry, "Couldn't look it up: "+err.Error())
		return 0
	case unknown:
		service.failed(entry, fmt.Sprintf("%s doesn't know this title (ID %d)", a.Name, entry.ExternalID))
		return 0
	case !released && at.Sub(now) > tries.ReleaseWindow:
		entry.State, entry.NextTry = NotReleased, at.Add(-tries.ReleaseWindow)
		entry.Result = "Not out yet (" + at.Format("2006-01-02") + "); monitored two days before"
		return 0
	}
	rootFolder, profileID, err := a.Destination()
	if err != nil {
		service.later(entry, err.Error())
		return 0
	}
	disk := service.diskStop
	if service.disk != nil {
		disk = service.disk
	}
	// A title already in Radarr or Sonarr fills its own folder's disk, which
	// may not be the destination's.
	folder := rootFolder
	if entry.ArrID != 0 {
		folder = service.folderOf(entry, rootFolder)
	}
	if stop := disk(cfg, folder, dryRun); stop != "" {
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
			what = "add it to " + a.Name + ", " + what
		}
		entry.Result = "Dry run: would " + what
		service.step(entry.Title, "Would "+what)
		return 0
	}
	if label := tagFor(entry.Source); label != "" {
		tagID, err := a.EnsureTag(label)
		if err != nil {
			service.later(entry, "Couldn't make the tag: "+err.Error())
			return 0
		}
		if entry.ArrID == 0 {
			service.step(entry.Title, "Adding it to "+a.Name+", in "+rootFolder)
			service.notedAdd()
			id, err := a.Add(rootFolder, profileID, tagID)
			if err != nil {
				service.later(entry, "Couldn't add it: "+err.Error())
				return 0
			}
			entry.ArrID = id
		} else if err := a.Tag(entry.ArrID, tagID); err != nil {
			service.later(entry, "Couldn't tag it: "+err.Error())
			return 0
		}
	}
	if err := a.Monitor(entry.ArrID, true); err != nil {
		service.later(entry, "Couldn't monitor it: "+err.Error())
		return 0
	}
	if !released {
		entry.State, entry.ReleaseAt, entry.MonitoredAt = NotReleased, at, now
		entry.NextTry = at.Add(tries.ReleaseGrace)
		entry.Result = "Monitored for its release on " + at.Format("2006-01-02")
		return 0
	}
	searches := entry.searches()
	started := time.Now()
	service.step(entry.Title, a.Name+" is searching its trackers")
	grabbed, err := a.Search(ctx, entry.ArrID)
	if err == nil && grabbed {
		service.downloading(entry, a, false)
		return searches
	}
	if unmonitorErr := a.Monitor(entry.ArrID, false); unmonitorErr != nil {
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

// failed counts a try that found nothing (or a title Radarr or Sonarr has
// no record of, often one too new for their sources): it's tried again
// after tries.RetryDelay, unless the lifecycle takes it out first.
// downloading marks a download confirmed and unmonitors the title, so
// Radarr and Sonarr don't upgrade it on their own: Overtide decides what
// comes in. It's imported all the same (neither checks monitoring to
// import). If unmonitoring fails, follow tries again.
func (service *Service) downloading(entry *Entry, a tries.Title, dryRun bool) {
	if !dryRun {
		if err := service.recordStarted(entry.searches()); err != nil {
			log.Printf("[adding] count the download of %s: %v", entry.Title, err)
		}
	}
	// A series that comes a season at a time, with seasons left: this one
	// goes its way (Sonarr imports it), and the next is tried at once, the
	// series first among the suggestions.
	if season := entry.trySeasons(); entry.bySeason() && len(entry.left()) > 1 {
		if !dryRun {
			if err := a.Monitor(entry.ArrID, false); err != nil {
				log.Printf("[adding] unmonitor %s season %d once grabbed: %v", entry.Title, season[0], err)
			}
		}
		entry.Grabbed = append(entry.Grabbed, season[0])
		entry.State, entry.Tries, entry.NextTry = Waiting, 0, service.now()
		entry.Result = fmt.Sprintf("Season %d downloading; season %d next", season[0], entry.left()[0])
		return
	}
	entry.State, entry.Result = Downloading, "Downloading"
	if entry.bySeason() {
		if season := entry.trySeasons(); len(season) == 1 && len(entry.left()) == 1 {
			entry.Result = fmt.Sprintf("Downloading season %d, the last", season[0])
		}
	}
	entry.Unmonitored = !dryRun && a.Monitor(entry.ArrID, false) == nil
}

func (service *Service) failed(entry *Entry, why string) {
	entry.State = Waiting
	entry.Tries++
	entry.NextTry = service.now().Add(tries.RetryDelay(entry.Tries))
	entry.Result = fmt.Sprintf("%s (try %d)", why, entry.Tries)
}

func (service *Service) later(entry *Entry, why string) {
	entry.NextTry, entry.Result = service.now().Add(tries.LaterOnError), why
}

// diskStop says why nothing may be added to the root folder now, or "":
// Room's answer (Overtide's, which knows the disks). Without Room, nothing
// stops adding.
func (service *Service) diskStop(cfg config.Config, rootFolder string, dryRun bool) string {
	if service.Room == nil {
		return ""
	}
	return service.Room(rootFolder, dryRun)
}

// folderOf is a title's folder, as last read, else the folder given.
func (service *Service) folderOf(entry *Entry, fallback string) string {
	items, _, err := service.inv.Snapshot()
	if err != nil {
		return fallback
	}
	for _, m := range items {
		if m.ServiceID == entry.ServiceID && m.Type == entry.Kind && m.SourceID == entry.ArrID && m.Path != "" {
			return m.Path
		}
	}
	return fallback
}
