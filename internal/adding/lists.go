package adding

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sort"
	"strconv"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
)

// The operator's import lists, with "Manage all": Overfin decides what of
// them comes, so the lists add nothing on their own and fetch nothing.
// Radarr keeps what its enabled lists have without adding it, and gives it
// (radarr.ListMovies): its lists add nothing. Sonarr doesn't: its lists
// that add series add them unmonitored, without searching, tagged
// model.TagList, and those tagged series are its lists' titles. Every list
// is sent to Overfin (overfin.SendLists), and comes back in its
// suggestions. A list the operator switched off stays off.

// sendLists takes the import lists over (in dry run, it only says so) and
// sends their titles to Overfin. Without "Manage all", the lists are the
// operator's, and nothing is sent.
func (service *Service) sendLists(ctx context.Context, cfg config.Config, dryRun bool) error {
	if !cfg.Adding.ManagesAll() || cfg.Overfin.URL == "" {
		return nil
	}
	var lists []overfin.List
	for _, svc := range cfg.ServicesOfType("radarr") {
		found, err := service.radarrLists(ctx, svc, dryRun)
		if err != nil {
			return fmt.Errorf("%s's import lists: %w", svc.Name, err)
		}
		lists = append(lists, found...)
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		found, err := service.sonarrLists(ctx, svc, dryRun)
		if err != nil {
			return fmt.Errorf("%s's import lists: %w", svc.Name, err)
		}
		lists = append(lists, found...)
	}
	return service.inv.OverfinClient().WithContext(ctx).SendLists(lists)
}

// listName is how Overfin shows a list: the app's name in Overtide, then
// the list's.
func listName(svc config.Service, name string) string { return svc.Name + ": " + name }

// radarrLists switches adding off on Radarr's lists, and returns their
// films, each list in its order.
func (service *Service) radarrLists(ctx context.Context, svc config.Service, dryRun bool) ([]overfin.List, error) {
	client, err := service.inv.RadarrClient(svc.ID)
	if err != nil {
		return nil, err
	}
	client = client.WithContext(ctx)
	all, err := client.ImportLists()
	if err != nil {
		return nil, err
	}
	names := map[int]string{}
	var order []int
	for _, list := range all {
		id, _ := list["id"].(float64)
		if on, _ := list["enabled"].(bool); !on {
			continue
		}
		name, _ := list["name"].(string)
		names[int(id)] = name
		order = append(order, int(id))
		if adds, _ := list["enableAuto"].(bool); !adds {
			if searches, _ := list["searchOnAdd"].(bool); !searches {
				continue
			}
		}
		if dryRun {
			log.Printf("[adding] dry run: %s's list %q would stop adding and searching", svc.Name, name)
			continue
		}
		list["enableAuto"], list["searchOnAdd"] = false, false
		if err := client.SaveImportList(list); err != nil {
			return nil, err
		}
		log.Printf("[adding] %s's list %q no longer adds or searches: Overfin decides what of it comes", svc.Name, name)
	}
	if len(order) == 0 {
		return nil, nil
	}
	movies, err := client.ListMovies()
	if err != nil {
		return nil, err
	}
	titles := map[int][]overfin.ListTitle{}
	for _, m := range movies {
		if m.TMDBID <= 0 {
			continue
		}
		for _, id := range m.Lists {
			titles[id] = append(titles[id], overfin.ListTitle{Kind: "movie", TMDB: strconv.Itoa(m.TMDBID)})
		}
	}
	var out []overfin.List
	for _, id := range order {
		out = append(out, overfin.List{Name: listName(svc, names[id]), Titles: titles[id]})
	}
	return out, nil
}

// sonarrLists has Sonarr's lists that add series add them unmonitored,
// without searching, tagged model.TagList, and returns the tagged series,
// the latest added first, as one list: Sonarr doesn't say which list
// brought a series, nor where it stands on it.
func (service *Service) sonarrLists(ctx context.Context, svc config.Service, dryRun bool) ([]overfin.List, error) {
	client, err := service.inv.SonarrClient(svc.ID)
	if err != nil {
		return nil, err
	}
	client = client.WithContext(ctx)
	all, err := client.ImportLists()
	if err != nil {
		return nil, err
	}
	labels, err := client.TagLabels()
	if err != nil {
		return nil, err
	}
	tagID := 0
	for id := range labels {
		if model.HasLabel([]int{id}, labels, model.TagList) {
			tagID = id
		}
	}
	adding := 0
	for _, list := range all {
		if adds, _ := list["enableAutomaticAdd"].(bool); !adds {
			continue
		}
		adding++
		name, _ := list["name"].(string)
		tags, _ := list["tags"].([]any)
		search, _ := list["searchForMissingEpisodes"].(bool)
		monitor, _ := list["shouldMonitor"].(string)
		newItems, _ := list["monitorNewItems"].(string)
		if tagID != 0 && slices.Contains(tags, any(float64(tagID))) && !search && monitor == "none" && newItems == "none" {
			continue
		}
		if dryRun {
			log.Printf("[adding] dry run: %s's list %q would add series unmonitored, without searching, tagged %s", svc.Name, name, model.TagList)
			continue
		}
		if tagID == 0 {
			if tagID, err = client.EnsureTag(model.TagList); err != nil {
				return nil, err
			}
			labels[tagID] = model.TagList
		}
		if !slices.Contains(tags, any(float64(tagID))) {
			list["tags"] = append(tags, tagID)
		}
		list["searchForMissingEpisodes"], list["shouldMonitor"], list["monitorNewItems"] = false, "none", "none"
		if err := client.SaveImportList(list); err != nil {
			return nil, err
		}
		log.Printf("[adding] %s's list %q now adds series unmonitored, without searching, tagged %s: Overfin decides what of it comes", svc.Name, name, model.TagList)
	}
	if adding == 0 || tagID == 0 {
		return nil, nil
	}
	series, err := client.Titles()
	if err != nil {
		return nil, err
	}
	sort.SliceStable(series, func(i, j int) bool { return series[i].Added.After(series[j].Added) })
	list := overfin.List{Name: listName(svc, "import lists")}
	for _, s := range series {
		if s.TMDBID > 0 && model.HasLabel(s.Tags, labels, model.TagList) {
			list.Titles = append(list.Titles, overfin.ListTitle{Kind: "series", TMDB: strconv.Itoa(s.TMDBID)})
		}
	}
	return []overfin.List{list}, nil
}
