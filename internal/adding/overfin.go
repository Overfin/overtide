package adding

import (
	"context"
	"log"
	"sort"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
)

// suggestionsMax is how many titles each of Overfin's suggestions brings
// in: its picks, by value, and each discovery list.
const suggestionsMax = 200

// discoveryLists are TMDB's lists the discovery share comes from, through
// Overfin, with how the Adding page names them.
var discoveryLists = []struct{ list, name string }{{"trending", "Trending"}, {"popular", "Popular"}}

// readOverfin brings Overfin's suggestions into the waiting list: its
// picks (the titles the libraries lack, by value) and, with a discovery
// share, TMDB's trending and popular titles. Films go to the first
// Radarr, series to the first Sonarr; a series Overfin knows no TheTVDB ID
// for is left out (Sonarr needs it). A suggestion that can't be read
// changes nothing of its titles; one Overfin no longer suggests leaves,
// unless it's Overtide's.
func (service *Service) readOverfin(ctx context.Context, cfg config.Config, entries []Entry) []Entry {
	if cfg.Overfin.URL == "" {
		return entries
	}
	client := service.inv.OverfinClient().WithContext(ctx)
	radarrs, sonarrs := cfg.ServicesOfType("radarr"), cfg.ServicesOfType("sonarr")
	byKey := map[string]int{}
	for i, entry := range entries {
		byKey[entry.ServiceID+"|"+entry.Key] = i
	}
	now := service.now()
	seen := map[string]bool{}
	read := map[Source]bool{}
	take := func(source Source, from string, found []overfin.Missing) {
		read[source] = true
		for position, m := range found {
			entry := Entry{Kind: m.Kind, Title: m.Name, Source: source, From: from, Position: position}
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
				continue // a pick already, or in the other list
			}
			seen[id] = true
			if i, ok := byKey[id]; ok {
				// Requests and handed-over titles keep their place; the
				// import lists' give way to Overfin's.
				if old := &entries[i]; old.Source == FromOverfin || old.Source == FromDiscovery || old.Source == FromList {
					old.Source, old.From, old.Position = source, from, position
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
	}
	if picks, err := client.Suggestions("", suggestionsMax); err != nil {
		log.Printf("[adding] Overfin's picks: %v", err)
	} else {
		take(FromOverfin, "Overfin", picks)
	}
	if cfg.Adding.Discovery() > 0 {
		ok := true
		for _, l := range discoveryLists {
			titles, err := client.Suggestions(l.list, suggestionsMax)
			if err != nil {
				log.Printf("[adding] Overfin's %s titles: %v", l.list, err)
				ok = false
				break
			}
			take(FromDiscovery, l.name, titles)
		}
		if !ok {
			delete(read, FromDiscovery)
		}
	} else {
		read[FromDiscovery] = true // none wanted: they all leave
	}
	kept := entries[:0]
	for _, entry := range entries {
		if read[entry.Source] && !entry.Ours && !seen[entry.ServiceID+"|"+entry.Key] {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// interleave orders Overfin's suggestions: of every hundred titles, share
// come from discovery (trending and popular side by side, by place), the
// rest are picks by value. Either fills in when the other runs out.
func interleave(picks, discovered []Entry, share int) []Entry {
	sort.SliceStable(picks, func(i, j int) bool { return picks[i].Position < picks[j].Position })
	sort.SliceStable(discovered, func(i, j int) bool {
		if discovered[i].Position != discovered[j].Position {
			return discovered[i].Position < discovered[j].Position
		}
		return discovered[i].From > discovered[j].From // Trending before Popular
	})
	out := make([]Entry, 0, len(picks)+len(discovered))
	taken := 0
	for len(picks) > 0 || len(discovered) > 0 {
		if len(discovered) > 0 && (len(picks) == 0 || taken*100 < share*(len(out)+1)) {
			out = append(out, discovered[0])
			discovered = discovered[1:]
			taken++
			continue
		}
		out = append(out, picks[0])
		picks = picks[1:]
	}
	return out
}
