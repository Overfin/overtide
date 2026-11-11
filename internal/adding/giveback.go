package adding

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/sonarrdriver"
)

// Giving titles back: a title the operator tags overtide-ignore, and,
// while Overtide manages only what it added, every title "Manage all" took
// (TagFound), goes back to Radarr or Sonarr as it was before Overtide took
// it: its monitoring as recorded then (sonarrdriver.Taken; a title taken
// before monitoring was recorded keeps the one it has), out of Overtide's
// lists, without Overtide's tags (an ignored one keeps overtide-keep and
// overtide-ignore; one given back keeps its others).

// tagEditor is what giveBack needs of a Radarr or a Sonarr.
type tagEditor struct {
	serviceID string
	kind      model.MediaType
	labels    func() (map[int]string, error)
	tags      func() (map[int][]int, error) // title → tag IDs
	editTags  func(ids []int, tagID int, add bool) error
	restore   func(id int, taken sonarrdriver.TakenTitle) error
}

// giveBack gives back what's due, and returns the list without those
// titles. In dry run, nothing changes.
func (service *Service) giveBack(ctx context.Context, cfg config.Config, entries []Entry, dryRun bool) ([]Entry, error) {
	if dryRun {
		return entries, nil
	}
	taken, err := service.sonarr.Taken()
	if err != nil {
		return entries, err
	}
	manageAll := cfg.Adding.ManagesAll()
	var editors []tagEditor
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			return entries, err
		}
		client = client.WithContext(ctx)
		editors = append(editors, tagEditor{serviceID: svc.ID, kind: model.Movie, labels: client.TagLabels,
			tags: func() (map[int][]int, error) {
				titles, err := client.Titles()
				out := map[int][]int{}
				for _, t := range titles {
					out[t.ID] = t.Tags
				}
				return out, err
			},
			editTags: client.EditTags,
			restore:  func(id int, t sonarrdriver.TakenTitle) error { return client.SetMonitored(id, t.Monitored) },
		})
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return entries, err
		}
		client = client.WithContext(ctx)
		editors = append(editors, tagEditor{serviceID: svc.ID, kind: model.Series, labels: client.TagLabels,
			tags: func() (map[int][]int, error) {
				titles, err := client.Titles()
				out := map[int][]int{}
				for _, t := range titles {
					out[t.ID] = t.Tags
				}
				return out, err
			},
			editTags: client.EditTags,
			restore: func(id int, t sonarrdriver.TakenTitle) error {
				if t.Series == nil {
					return nil
				}
				return client.RestoreMonitoring(id, *t.Series)
			},
		})
	}

	gone := map[string]bool{}
	for _, e := range editors {
		// A service that doesn't answer gives back at the next run; the
		// others, and the run, go on.
		labels, err := e.labels()
		if err != nil {
			log.Printf("[adding] giving titles back to %s: %v", e.serviceID, err)
			continue
		}
		titles, err := e.tags()
		if err != nil {
			log.Printf("[adding] giving titles back to %s: %v", e.serviceID, err)
			continue
		}
		// The tag IDs each title loses, by tag.
		drop := map[int][]int{}
		for id, tags := range titles {
			ignored := model.HasLabel(tags, labels, model.TagIgnore)
			found := model.HasLabel(tags, labels, model.TagFound)
			if !ignored && (manageAll || !found) {
				continue
			}
			key := sonarrdriver.TakenKey(e.serviceID, e.kind, id)
			if t, ok := taken[key]; ok {
				if err := e.restore(id, t); err != nil {
					log.Printf("[adding] giving %s back: %v", key, err)
					continue // tried again at the next run
				}
				if err := service.sonarr.ForgetTaken(key); err != nil {
					return entries, err
				}
			}
			for _, tag := range tags {
				label := labels[tag]
				switch {
				case ignored && model.IsManagedTag(label):
					drop[tag] = append(drop[tag], id) // all of Overtide's but keep and ignore
				case !ignored && model.HasLabel([]int{tag}, labels, model.TagFound):
					drop[tag] = append(drop[tag], id)
				}
			}
			gone[fmt.Sprintf("%s|%s|%d", e.serviceID, e.kind, id)] = true
			if e.kind == model.Series {
				if err := service.sonarr.ForgetSeries(e.serviceID, id); err != nil {
					return entries, err
				}
			}
		}
		for tag, ids := range drop {
			sort.Ints(ids)
			if err := e.editTags(ids, tag, false); err != nil {
				log.Printf("[adding] giving titles back to %s: %v", e.serviceID, err)
				continue
			}
			log.Printf("[adding] %d titles given back: tag %q taken off", len(ids), labels[tag])
		}
	}
	kept := entries[:0]
	for _, entry := range entries {
		if gone[fmt.Sprintf("%s|%s|%d", entry.ServiceID, entry.Kind, entry.ArrID)] {
			continue
		}
		kept = append(kept, entry)
	}
	return kept, nil
}
