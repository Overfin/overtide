package adding

import (
	"context"
	"log"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// suggestionsMax is how many of Overfin's suggestions are read: more than
// WaitingMax, since some are skipped (in Radarr or Sonarr already, in their
// cooldown).
const suggestionsMax = 400

// ReadOverfin is the Overfin suggestions task: it records what left the
// libraries since, then brings what Overfin suggests adding into the
// waiting list, in Overfin's order.
func (service *Service) ReadOverfin(ctx context.Context) error {
	cfg := service.inv.Config()
	if cfg.Adding.Mode != config.AddingDryRun && cfg.Adding.Mode != config.AddingOn {
		return nil
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	entries, err := service.load()
	if err != nil {
		return err
	}
	history, err := service.history()
	if err != nil {
		return err
	}
	if err := service.recordLeaves(history); err != nil {
		return err
	}
	if err := service.saveHistory(history); err != nil {
		return err
	}
	return service.save(service.readOverfin(ctx, cfg, entries, history))
}

// readOverfin brings Overfin's suggestions into the waiting list, in
// Overfin's order: films to the first Radarr, series (those Overfin knows
// a TheTVDB ID for, Sonarr needs it) to the first Sonarr, with the seasons
// suggested. A new title is taken only while fewer than WaitingMax
// suggestions wait, and not during its cooldown. If the suggestions can't
// be read, nothing changes; one Overfin no longer suggests leaves, unless
// it's moving (downloading, imported, monitored for its release).
func (service *Service) readOverfin(ctx context.Context, cfg config.Config, entries []Entry, history map[string]historyEvent) []Entry {
	if cfg.Overfin.URL == "" {
		return entries
	}
	found, err := service.inv.OverfinClient().WithContext(ctx).Suggestions(suggestionsMax)
	if err != nil {
		log.Printf("[adding] Overfin's suggestions: %v", err)
		return entries
	}
	radarrs, sonarrs := cfg.ServicesOfType("radarr"), cfg.ServicesOfType("sonarr")
	byID := map[string]int{}
	waiting := 0
	for i, entry := range entries {
		byID[entry.id()] = i
		if entry.Source == FromOverfin && entry.waiting() {
			waiting++
		}
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
			entry.Seasons = m.Seasons
		default:
			continue
		}
		id := entry.id()
		if seen[id] {
			continue
		}
		seen[id] = true
		if i, ok := byID[id]; ok {
			// Requests keep their place; Overfin's titles take their new one.
			if old := &entries[i]; old.Source == FromOverfin {
				old.Position = position
				if old.Title == "" {
					old.Title = entry.Title
				}
				if old.waiting() && len(entry.Seasons) > 0 {
					old.Seasons = entry.Seasons
				}
			}
			continue
		}
		if _, cooling := history[id]; cooling || waiting >= WaitingMax {
			continue
		}
		entry.State, entry.NextTry, entry.AddedAt, entry.UpdatedAt = Waiting, now, now, now
		byID[id] = len(entries)
		entries = append(entries, entry)
		waiting++
	}
	kept := entries[:0]
	for _, entry := range entries {
		if entry.Source == FromOverfin && entry.waiting() && !seen[entry.id()] {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}
