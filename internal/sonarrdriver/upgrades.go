package sonarrdriver

import (
	"context"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/tries"
)

// DuePack is a season that came complete without its whole-season pack
// (its episodes one by one: seekPack), due for a search of one now.
type DuePack struct {
	ServiceID        string
	SeriesID, Season int
}

// DuePacks lists the seasons whose pack may be searched now, but those the
// operator removed. The caller orders them with the other upgrades, by
// value, and tries each with PackUpgrade.
func (driver *Driver) DuePacks() ([]DuePack, error) {
	states, err := driver.States()
	if err != nil {
		return nil, err
	}
	now := driver.now()
	removed := driver.Removed()
	var out []DuePack
	for serviceID, state := range states {
		if state == nil {
			continue
		}
		wanting := map[string]bool{}
		for _, want := range state.Wanted {
			wanting[seasonKey(want.SeriesID, want.Season)] = true
		}
		for key, pack := range state.Seasons {
			if pack.Done || pack.short() || !pack.GrabbedAt.IsZero() || (pack.Tries == 0 && !pack.Upgrade) || pack.Tries >= tries.Max || pack.NextTry.After(now) || wanting[key] {
				continue
			}
			if _, out := removed.Upgrades[PackRef(serviceID, pack.SeriesID, pack.Season)]; out {
				continue // removed by the operator
			}
			out = append(out, DuePack{ServiceID: serviceID, SeriesID: pack.SeriesID, Season: pack.Season})
		}
	}
	return out, nil
}

// PackUpgrade looks for the whole-season pack of a due season, counted as
// an upgrade (Upgraded). A pack sent replaces its files as any other
// (followPack), so the season comes from one release; none found, it's
// tried again later, tries.Max in all. searched says whether a search went
// (not when the disk, the folder's upgrades a day or the season's episodes
// hold it back).
func (driver *Driver) PackUpgrade(ctx context.Context, cfg config.Config, due DuePack) (searched bool, err error) {
	if driver.Stopped() || !driver.Ready() {
		return false, nil
	}
	states, err := driver.States()
	if err != nil {
		return false, err
	}
	state := states[due.ServiceID]
	if state == nil {
		return false, nil
	}
	pack := state.Seasons[seasonKey(due.SeriesID, due.Season)]
	if pack == nil {
		return false, nil
	}
	folders := driver.seriesFolders(due.ServiceID)
	if driver.fullDisk(cfg, due.ServiceID)(due.SeriesID) || !driver.UpgradeRoom(cfg, folders[due.SeriesID]) {
		return false, nil
	}
	client, err := driver.inv.SonarrClient(due.ServiceID)
	if err != nil {
		return false, err
	}
	client = client.WithContext(ctx)
	list, err := client.Episodes(due.SeriesID)
	if err != nil || !seasonOut(list, due.Season, driver.now()) {
		return false, nil
	}
	driver.seekPack(ctx, cfg, due.ServiceID, client, state, pack, list, driver.seriesNames(due.ServiceID)[due.SeriesID], true)
	if err := driver.Upgraded(folders[due.SeriesID]); err != nil {
		return true, err
	}
	return true, driver.SaveStates(states)
}
