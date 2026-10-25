package adding

import (
	"context"
	"log"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// suggestionsMax is how many of Overfin's suggestions are taken.
const suggestionsMax = 400

// readOverfin brings Overfin's suggestions into the waiting list, in
// Overfin's order (its picks by value, with trending and popular titles
// mixed in by its discovery share). Films go to the first Radarr, series to
// the first Sonarr; a series Overfin knows no TheTVDB ID for is left out
// (Sonarr needs it). If the suggestions can't be read, nothing changes;
// a title Overfin no longer suggests leaves, unless it's Overtide's.
func (service *Service) readOverfin(ctx context.Context, cfg config.Config, entries []Entry) []Entry {
	if cfg.Overfin.URL == "" {
		return entries
	}
	found, err := service.inv.OverfinClient().WithContext(ctx).Suggestions(suggestionsMax)
	if err != nil {
		log.Printf("[adding] Overfin's suggestions: %v", err)
		return entries
	}
	radarrs, sonarrs := cfg.ServicesOfType("radarr"), cfg.ServicesOfType("sonarr")
	byKey := map[string]int{}
	for i, entry := range entries {
		byKey[entry.ServiceID+"|"+entry.Key] = i
	}
	now := service.now()
	seen := map[string]bool{}
	for position, m := range found {
		entry := Entry{Kind: m.Kind, Title: m.Name, Source: FromOverfin, Position: position}
		switch {
		case m.Kind == model.Movie && len(radarrs) > 0:
			entry.Key, entry.ServiceID, entry.ExternalID = movieKey(m.TMDB), radarrs[0].ID, m.TMDB
		case m.Kind == model.Series && len(sonarrs) > 0 && m.TVDB > 0:
			entry.Key, entry.ServiceID, entry.ExternalID = seriesKey(m.TVDB), sonarrs[0].ID, m.TVDB
		default:
			continue
		}
		id := entry.ServiceID + "|" + entry.Key
		if seen[id] {
			continue
		}
		seen[id] = true
		if i, ok := byKey[id]; ok {
			// Requests keep their place; Overfin's titles take their new one.
			if old := &entries[i]; old.Source == FromOverfin {
				old.Position = position
				if old.Title == "" {
					old.Title = entry.Title
				}
			}
			continue
		}
		entry.State, entry.NextTry, entry.UpdatedAt = Waiting, now, now
		byKey[id] = len(entries)
		entries = append(entries, entry)
	}
	kept := entries[:0]
	for _, entry := range entries {
		if entry.Source == FromOverfin && !entry.Ours && !seen[entry.ServiceID+"|"+entry.Key] {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}
