package adding

import (
	"context"
	"log"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// readLists brings the import lists' titles into the waiting list: new
// ones wait for a try, the rest keep their state. A title no list holds any
// more leaves the list, unless it's Overtide's. A list that can't be read
// changes nothing of its service's titles.
func (service *Service) readLists(ctx context.Context, cfg config.Config, entries []Entry) ([]Entry, error) {
	byKey := map[string]int{}
	for i, entry := range entries {
		byKey[entry.ServiceID+"|"+entry.Key] = i
	}
	seen := map[string]bool{}
	read := map[string]bool{}
	now := service.now()
	upsert := func(found Entry) {
		id := found.ServiceID + "|" + found.Key
		seen[id] = true
		if i, ok := byKey[id]; ok {
			entries[i].Title, entries[i].Year = found.Title, found.Year
			entries[i].From, entries[i].ListID, entries[i].Position = found.From, found.ListID, found.Position
			return
		}
		found.Source, found.State, found.NextTry, found.UpdatedAt = FromList, Waiting, now, now
		byKey[id] = len(entries)
		entries = append(entries, found)
	}

	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			continue
		}
		client = client.WithContext(ctx)
		lists, err := client.ImportLists()
		if err != nil {
			log.Printf("[adding] %s import lists: %v", svc.Name, err)
			continue
		}
		names := map[int]string{}
		for _, list := range lists {
			names[list.ID] = list.Name
		}
		movies, err := client.ListMovies()
		if err != nil {
			log.Printf("[adding] %s list contents: %v", svc.Name, err)
			continue
		}
		read[svc.ID] = true
		for position, movie := range movies {
			if movie.IsExcluded || movie.TMDBID <= 0 || len(movie.Lists) == 0 {
				continue
			}
			upsert(Entry{Key: movieKey(movie.TMDBID), Kind: model.Movie, ServiceID: svc.ID, ExternalID: movie.TMDBID, Title: movie.Title, Year: movie.Year, From: names[movie.Lists[0]], ListID: movie.Lists[0], Position: position})
		}
	}

	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			continue
		}
		client = client.WithContext(ctx)
		lists, err := client.ImportLists()
		if err != nil {
			log.Printf("[adding] %s import lists: %v", svc.Name, err)
			continue
		}
		excluded, err := client.ExcludedTVDBIDs()
		if err != nil {
			log.Printf("[adding] %s list exclusions: %v", svc.Name, err)
			continue
		}
		complete := true
		for _, list := range lists {
			address := list.CustomURL()
			if address == "" {
				// Only custom lists can be read without Sonarr.
				continue
			}
			series, err := sonarr.FetchCustomList(ctx, address)
			if err != nil {
				log.Printf("[adding] %s list %s: %v", svc.Name, list.Name, err)
				complete = false
				continue
			}
			for position, item := range series {
				if item.TVDBID <= 0 || excluded[item.TVDBID] {
					continue
				}
				upsert(Entry{Key: seriesKey(item.TVDBID), Kind: model.Series, ServiceID: svc.ID, ExternalID: item.TVDBID, Title: item.Title, Year: item.Year, From: list.Name, ListID: list.ID, Position: position})
			}
		}
		read[svc.ID] = complete
	}

	kept := entries[:0]
	for _, entry := range entries {
		gone := read[entry.ServiceID] && !seen[entry.ServiceID+"|"+entry.Key]
		if gone && !entry.Ours && entry.Source == FromList {
			continue
		}
		kept = append(kept, entry)
	}
	return kept, nil
}
