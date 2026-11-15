package sonarrdriver

import (
	"context"
	"log"

	"github.com/overfin/overtide/internal/config"
)

// SettleLooks takes off "Needs a look" the seasons that came complete
// since: a pack that came short, or a download with files matching none of
// Sonarr's episodes, whose season now has the file of every episode aired
// (a later release, an import by hand, a repair). It only reads Sonarr, so
// it runs whatever adding's mode, and its list stays true.
func (driver *Driver) SettleLooks(ctx context.Context, cfg config.Config) {
	states, err := driver.States()
	if err != nil {
		return
	}
	now := driver.now()
	changed := false
	for _, svc := range cfg.ServicesOfType("sonarr") {
		state := states[svc.ID]
		if state == nil {
			continue
		}
		client, err := driver.inv.SonarrClient(svc.ID)
		if err != nil {
			continue
		}
		client = client.WithContext(ctx)
		for _, pack := range state.Seasons {
			if !pack.short() || ctx.Err() != nil {
				continue
			}
			episodes, err := client.Episodes(pack.SeriesID)
			if err != nil {
				log.Printf("[adding] the episodes of a season that needs a look: %v", err)
				continue
			}
			complete, aired := true, 0
			for _, e := range episodes {
				if e.SeasonNumber != pack.Season || e.AirDateUTC.IsZero() || e.AirDateUTC.After(now) {
					continue
				}
				aired++
				complete = complete && e.HasFile
			}
			if aired > 0 && complete {
				pack.Short, pack.Unmatched, pack.Done = 0, nil, true
				changed = true
			}
		}
	}
	if changed {
		if err := driver.SaveStates(states); err != nil {
			log.Printf("[adding] seasons settled: %v", err)
		}
	}
}
