package sonarrdriver

import (
	"context"
	"sort"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/tries"
)

// PackUpgrades looks for the whole-season packs of the seasons that came
// complete without one (their episodes one by one: seekPack), most valued
// first, at most room searches, each counted as an upgrade (Upgraded). A
// pack sent replaces their files as any other (followPack), so the season
// comes from one release; none found, it's tried again later, tries.Max in
// all. It returns the searches it asked for.
func (driver *Driver) PackUpgrades(ctx context.Context, cfg config.Config, room int) (int, error) {
	states, err := driver.States()
	if err != nil {
		return 0, err
	}
	now := driver.now()
	used := 0
	for _, svc := range cfg.ServicesOfType("sonarr") {
		state := states[svc.ID]
		if state == nil {
			continue
		}
		wanting := map[string]bool{}
		for _, want := range state.Wanted {
			wanting[seasonKey(want.SeriesID, want.Season)] = true
		}
		removed := driver.Removed()
		var due []*SeasonPack
		for key, pack := range state.Seasons {
			if pack.Done || pack.short() || !pack.GrabbedAt.IsZero() || pack.Tries == 0 || pack.Tries >= tries.Max || pack.NextTry.After(now) || wanting[key] {
				continue
			}
			if _, out := removed.Upgrades[PackRef(svc.ID, pack.SeriesID, pack.Season)]; out {
				continue // removed by the operator
			}
			due = append(due, pack)
		}
		if len(due) == 0 {
			continue
		}
		sort.SliceStable(due, func(i, j int) bool {
			return driver.SeasonValue(ctx, cfg, svc.ID, due[i].SeriesID, due[i].Season) > driver.SeasonValue(ctx, cfg, svc.ID, due[j].SeriesID, due[j].Season)
		})
		client, err := driver.inv.SonarrClient(svc.ID)
		if err != nil {
			return used, err
		}
		client = client.WithContext(ctx)
		full := driver.fullDisk(cfg, svc.ID)
		names := driver.seriesNames(svc.ID)
		for _, pack := range due {
			if used >= room || ctx.Err() != nil || driver.Stopped() || !driver.Ready() {
				return used, nil
			}
			if full(pack.SeriesID) {
				continue
			}
			list, err := client.Episodes(pack.SeriesID)
			if err != nil || !seasonOut(list, pack.Season, now) {
				continue
			}
			used++
			driver.seekPack(ctx, cfg, svc.ID, client, state, pack, list, names[pack.SeriesID])
			if err := driver.Upgraded(); err != nil {
				return used, err
			}
			if err := driver.SaveStates(states); err != nil {
				return used, err
			}
		}
	}
	return used, nil
}
