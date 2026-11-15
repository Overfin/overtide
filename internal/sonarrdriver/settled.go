package sonarrdriver

import (
	"context"
	"log"

	"github.com/overfin/overtide/internal/config"
)

// SettleLooks takes off "Needs a look" the seasons that came complete
// since: a pack that came short, or a download with files matching none of
// Sonarr's episodes, whose season now has the file of every episode aired
// (a later release, an import by hand, a repair); and the seasons noted
// for a download no longer in the queue (their missing episodes are then
// wanted one by one, as any). It only reads Sonarr, so
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
		// Files matching none of Sonarr's episodes are noted of a download
		// in the queue: once none there holds that season, the note goes
		// (its files imported by hand or by Overtide, or the download
		// gone). A queue that can't be read clears nothing.
		var inQueue map[string]bool
		for _, pack := range state.Seasons {
			if len(pack.Unmatched) == 0 || ctx.Err() != nil {
				continue
			}
			if inQueue == nil {
				queue, err := client.FullQueue()
				if err != nil {
					break
				}
				inQueue = map[string]bool{}
				for _, item := range queue {
					inQueue[seasonKey(item.SeriesID, item.SeasonNumber)] = true
				}
			}
			if !inQueue[seasonKey(pack.SeriesID, pack.Season)] {
				pack.Unmatched = nil
				changed = true
			}
		}
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
