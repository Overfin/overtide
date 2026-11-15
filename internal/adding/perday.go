package adding

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// startedKey keeps when each title started downloading over the last 24
// hours (a series, once per season), a list per root folder (folderKey):
// the daily limit.
const startedKey = "adding.started"

// folderKey is the key of a root folder's list under key: each root
// folder has its own daily limits.
func folderKey(key, root string) string {
	return key + "|" + root
}

// rootOf is the root folder holding path, "" when unknown (the default
// limits then).
func (service *Service) rootOf(path string) string {
	return service.inv.RootFolderOf(path)
}

// started returns when titles started downloading in a root folder over
// the last 24 hours, oldest first.
func (service *Service) started(root string) ([]time.Time, error) {
	return service.lastDay(folderKey(startedKey, root))
}

// lastDay returns the times kept under key, those of the last 24 hours,
// oldest first.
func (service *Service) lastDay(key string) ([]time.Time, error) {
	raw, err := service.inv.Store().Meta(key)
	if err != nil || raw == "" {
		return nil, err
	}
	var times []time.Time
	if err := json.Unmarshal([]byte(raw), &times); err != nil {
		return nil, fmt.Errorf("read the downloads started: %w", err)
	}
	since := service.now().Add(-24 * time.Hour)
	kept := times[:0]
	for _, t := range times {
		if t.After(since) {
			kept = append(kept, t)
		}
	}
	return kept, nil
}

// recordStarted counts n downloads started now in the root folder
// holding path.
func (service *Service) recordStarted(path string, n int) error {
	return service.recordNow(folderKey(startedKey, service.rootOf(path)), n)
}

// recordNow adds n times to those kept under key.
func (service *Service) recordNow(key string, n int) error {
	times, err := service.lastDay(key)
	if err != nil {
		return err
	}
	for range n {
		times = append(times, service.now())
	}
	encoded, err := json.Marshal(times)
	if err != nil {
		return err
	}
	return service.inv.Store().SetMeta(key, string(encoded))
}

// DailyLimit says whether n more titles may start downloading now in the
// root folder holding path, under its titles a day; if not, until when.
func (service *Service) DailyLimit(path string, n int) (bool, time.Time) {
	root := service.rootOf(path)
	times, err := service.started(root)
	if err != nil {
		return false, time.Time{}
	}
	limit := service.inv.Config().TitlesPerDayFor(root)
	if len(times)+n <= limit || len(times) == 0 {
		return true, time.Time{}
	}
	// Room comes back as the oldest ones turn 24 hours old.
	over := len(times) + n - limit
	if over > len(times) {
		over = len(times)
	}
	return false, times[over-1].Add(24 * time.Hour)
}

// FolderDay is a root folder's day: the titles started and upgrade
// searches of the last 24 hours, and its limits.
type FolderDay struct {
	Root                     string
	Titles, TitlesPerDay     int
	Upgrades, UpgradesPerDay int
	// TitlesUntil, when the day's titles are all used, is when the next
	// may start.
	TitlesUntil time.Time
}

// Days are the root folders' days.
func (service *Service) Days() []FolderDay {
	cfg := service.inv.Config()
	var out []FolderDay
	for _, root := range service.inv.RootFolders() {
		titles, _ := service.started(root)
		upgrades, _ := service.lastDay(folderKey(upgradesKey, root))
		day := FolderDay{Root: root, Titles: len(titles), TitlesPerDay: cfg.TitlesPerDayFor(root), Upgrades: len(upgrades), UpgradesPerDay: cfg.UpgradesPerDayFor(root)}
		if ok, until := service.DailyLimit(root, 1); !ok {
			day.TitlesUntil = until
		}
		out = append(out, day)
	}
	return out
}

// entryFolder is where a title goes, or is: its own folder once in Radarr
// or Sonarr, else its destination's root folder, else its service's only
// one; "" when none is known.
func (service *Service) entryFolder(cfg config.Config, entry *Entry) string {
	if entry.ArrID != 0 {
		if path := service.folderOf(entry, ""); path != "" {
			return path
		}
	}
	serviceType := "radarr"
	if entry.Kind == model.Series {
		serviceType = "sonarr"
	}
	if d, ok := cfg.DestinationFor(serviceType, entry.Categories); ok && d.ServiceID == entry.ServiceID {
		return d.RootFolder
	}
	if roots := service.inv.RootFoldersOf(entry.ServiceID); len(roots) == 1 {
		return roots[0]
	}
	return ""
}

// EntryLimit is DailyLimit for one more try of a title, in its folder.
func (service *Service) EntryLimit(entry Entry) (bool, time.Time) {
	return service.DailyLimit(service.entryFolder(service.inv.Config(), &entry), entry.searches())
}
