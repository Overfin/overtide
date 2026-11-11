package adding

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/sonarrdriver"
)

// What the operator doesn't want: titles removed from the waiting list by
// hand, those in Radarr's and Sonarr's import-list exclusions, and those
// removed from Radarr or Sonarr themselves.

// ErrNotInList: no such title in the waiting list.
var ErrNotInList = errors.New("not in the waiting list")

// ErrDownloading: a title downloading isn't removed from the list; its
// download is followed to its end.
var ErrDownloading = errors.New("downloading")

// RemoveTitle takes a title out of the waiting list by hand, at once (a
// run in progress doesn't bring it back), and for good: but for one
// imported, which only leaves the list, it goes in its Radarr's or
// Sonarr's import-list exclusions, where the operator can take it out
// again. Overtide keeps it out on its own meanwhile, should Radarr or
// Sonarr not answer.
func (service *Service) RemoveTitle(ctx context.Context, id string) error {
	entries, err := service.load()
	if err != nil {
		return err
	}
	var entry *Entry
	for i := range entries {
		if entries[i].id() == id {
			entry = &entries[i]
		}
	}
	switch {
	case entry == nil:
		return ErrNotInList
	case entry.State == Downloading:
		return ErrDownloading
	}
	if entry.State != Imported && entry.ExternalID > 0 {
		if err := service.exclude(ctx, *entry); err != nil {
			log.Printf("[adding] %q removed from the waiting list, but not added to the import-list exclusions: %v", entry.Title, err)
		}
	}
	return service.sonarr.Remove(func(r *sonarrdriver.Removed, now time.Time) { r.Titles[id] = now })
}

// exclude adds a title to its Radarr's or Sonarr's import-list
// exclusions.
func (service *Service) exclude(ctx context.Context, entry Entry) error {
	if entry.Kind == model.Series {
		client, err := service.inv.SonarrClient(entry.ServiceID)
		if err != nil {
			return err
		}
		return client.WithContext(ctx).AddImportListExclusion(entry.Title, entry.ExternalID)
	}
	client, err := service.inv.RadarrClient(entry.ServiceID)
	if err != nil {
		return err
	}
	return client.WithContext(ctx).AddImportListExclusion(entry.Title, entry.Year, entry.ExternalID)
}

// removedByHand reports whether the operator removed the entry by hand.
func (service *Service) removedByHand(entry Entry) bool {
	_, out := service.sonarr.Removed().Titles[entry.id()]
	return out && entry.State != Downloading
}

// excluded are the titles of every Radarr's and Sonarr's import-list
// exclusions, by entry ID; a service that doesn't answer has none.
func (service *Service) excluded(ctx context.Context, cfg config.Config) map[string]bool {
	out := map[string]bool{}
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			continue
		}
		ids, err := client.WithContext(ctx).Exclusions()
		if err != nil {
			log.Printf("[adding] %s's import-list exclusions: %v", svc.Name, err)
			continue
		}
		for id := range ids {
			out[svc.ID+"|"+movieKey(id)] = true
		}
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			continue
		}
		ids, err := client.WithContext(ctx).Exclusions()
		if err != nil {
			log.Printf("[adding] %s's import-list exclusions: %v", svc.Name, err)
			continue
		}
		for id := range ids {
			out[svc.ID+"|"+seriesKey(id)] = true
		}
	}
	return out
}

// presentKey keeps the titles Radarr and Sonarr had at the last look, by
// entry ID, to see those removed there since.
const presentKey = "adding.present"

// leftArrs takes out of the list the titles removed from Radarr or Sonarr
// since the last look (by hand, or by Overtide's removals): they go in the
// history, so Overfin's suggestions don't bring them back before the
// cooldown. Those downloading are followed as before. Nothing is decided
// on an inventory that isn't there.
func (service *Service) leftArrs(entries []Entry, history map[string]historyEvent) ([]Entry, bool) {
	items, _, err := service.inv.Snapshot()
	if err != nil || len(items) == 0 {
		return entries, false
	}
	now := map[string]bool{}
	for _, m := range items {
		if key := titleKey(m); key != "" {
			now[m.ServiceID+"|"+key] = true
		}
	}
	store := service.inv.Store()
	var before map[string]bool
	if raw, err := store.Meta(presentKey); err == nil && raw != "" {
		json.Unmarshal([]byte(raw), &before)
	}
	// An inventory with far fewer titles than the last is more likely
	// partial than emptied by hand: it decides nothing, and isn't kept.
	if len(before) > 0 && len(now) < len(before)/2 {
		return entries, false
	}
	if encoded, err := json.Marshal(now); err == nil {
		store.SetMeta(presentKey, string(encoded))
	}
	gone := map[string]bool{}
	for id := range before {
		if !now[id] {
			gone[id] = true
		}
	}
	if len(gone) == 0 {
		return entries, false
	}
	at := service.now()
	changed := false
	for id := range gone {
		history[id] = historyEvent{At: at, Why: "Removed from Radarr or Sonarr"}
		changed = true
	}
	kept := entries[:0]
	for _, entry := range entries {
		if gone[entry.id()] && entry.State != Downloading {
			log.Printf("[adding] %q was removed from %s: out of the waiting list", entry.Title, serviceName(entry))
			continue
		}
		kept = append(kept, entry)
	}
	return kept, changed
}

func serviceName(entry Entry) string {
	if entry.Kind == model.Series {
		return "Sonarr"
	}
	return "Radarr"
}

// dropUnwanted takes out of the list what the operator doesn't want: the
// titles removed by hand, and Overfin's suggestions in the import-list
// exclusions (excluded, nil when not read). Wishes and titles added by
// hand stay: someone asked for them.
func (service *Service) dropUnwanted(entries []Entry, excluded map[string]bool) []Entry {
	kept := entries[:0]
	for _, entry := range entries {
		if service.removedByHand(entry) {
			continue
		}
		if excluded[entry.id()] && entry.Source == FromOverfin && entry.waiting() {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// unwanted takes out of the list the titles removed from Radarr or Sonarr
// (leftArrs), those removed by hand, and Overfin's suggestions in the
// import-list exclusions.
func (service *Service) unwanted(ctx context.Context, cfg config.Config, entries []Entry) ([]Entry, error) {
	history, err := service.history()
	if err != nil {
		return entries, err
	}
	entries, changed := service.leftArrs(entries, history)
	if changed {
		if err := service.saveHistory(history); err != nil {
			return entries, err
		}
	}
	return service.dropUnwanted(entries, service.excluded(ctx, cfg)), nil
}
