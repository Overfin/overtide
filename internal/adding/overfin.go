package adding

import (
	"context"
	"log"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/tries"
)

// suggestionsMax is how many of Overfin's suggestions are read: more than
// WaitingMax, since some are skipped (in Radarr or Sonarr already, in their
// cooldown).
const suggestionsMax = 400

// ReadOverfin is the Overfin suggestions task: it reads what left the
// libraries (Overfin's history), then brings what people wished for into
// the waiting list, makes sure Radarr and Sonarr send their events
// (connect), then brings what Overfin suggests adding, in Overfin's order.
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
	service.recordLeft(ctx, cfg, history)
	if err := service.saveHistory(history); err != nil {
		return err
	}
	entries = service.readWishes(ctx, cfg, entries)
	if err := service.connect(ctx, cfg, cfg.Adding.Mode == config.AddingDryRun); err != nil {
		log.Printf("[adding] Radarr's and Sonarr's events: %v", err)
	}
	if entries, err = service.unwanted(ctx, cfg, service.readOverfin(ctx, cfg, entries, history)); err != nil {
		return err
	}
	return service.save(entries)
}

// readOverfin brings Overfin's suggestions into the waiting list, in
// Overfin's order: films to a Radarr, series (those Overfin knows a TheTVDB
// ID for, Sonarr needs it) to a Sonarr, with the seasons suggested; where
// their destination says (config.DestinationFor), else the first. A new title is taken only while fewer than WaitingMax
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
		entry := Entry{Kind: m.Kind, Title: m.Name, Source: FromOverfin, Position: position, Categories: m.Categories}
		switch {
		case m.Kind == model.Movie && len(radarrs) > 0:
			entry.Key, entry.ServiceID, entry.ExternalID = movieKey(m.TMDB), radarrs[0].ID, m.TMDB
			if d, ok := cfg.DestinationFor("radarr", m.Categories); ok {
				entry.ServiceID = d.ServiceID
			}
		case m.Kind == model.Series && len(sonarrs) > 0 && m.TVDB > 0:
			entry.Key, entry.ServiceID, entry.ExternalID = seriesKey(m.TVDB), sonarrs[0].ID, m.TVDB
			if d, ok := cfg.DestinationFor("sonarr", m.Categories); ok {
				entry.ServiceID = d.ServiceID
			}
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
				old.Position, old.Categories = position, entry.Categories
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

// recordLeft puts in the history every title that left the libraries
// within tries.Cooldown, as Overfin's history has it (the key's "What came
// and went"), at its last departure: it isn't taken again before the
// cooldown ends, whoever removed it. A film counts for every Radarr, a
// series for every Sonarr. If Overfin can't say, the history stays as it
// was.
func (service *Service) recordLeft(ctx context.Context, cfg config.Config, history map[string]historyEvent) {
	if cfg.Overfin.URL == "" {
		return
	}
	left, err := service.inv.OverfinClient().WithContext(ctx).LeftSince(service.now().Add(-tries.Cooldown))
	if err != nil {
		log.Printf("[adding] what left the libraries, from Overfin (its key needs \"What came and went\"): %v", err)
		return
	}
	for _, title := range left {
		serviceType, key := "radarr", movieKey(title.ID)
		if title.Kind == model.Series {
			serviceType, key = "sonarr", seriesKey(title.ID)
		}
		for _, svc := range cfg.ServicesOfType(serviceType) {
			id := svc.ID + "|" + key
			if event, ok := history[id]; ok && !title.At.After(event.At) {
				continue
			}
			history[id] = historyEvent{At: title.At, Why: "Left the libraries"}
		}
	}
}
