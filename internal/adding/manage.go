package adding

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

const (
	// manageAllKey records when "Manage all" first took the libraries: a
	// title added after it is TagNew, one there before TagFound.
	manageAllKey = "adding.manage_all_at"
	// summaryKey keeps what the last "Manage all" pass did, or would do.
	summaryKey = "adding.manage_summary"
)

// ManageSummary is what the last "Manage all" pass did, or would do in dry
// run: shown on the Adding page.
type ManageSummary struct {
	At     time.Time `json:"at"`
	DryRun bool      `json:"dryRun"`
	// Tagged: titles that got one of adding's tags; Unmonitored: films
	// Radarr no longer looks for on its own; SeriesTaken: series Sonarr no
	// longer grabs for on its own.
	Tagged      int `json:"tagged"`
	Unmonitored int `json:"unmonitored"`
	SeriesTaken int `json:"seriesTaken"`
	// FilmsQueued: monitored films without their file, put in line.
	FilmsQueued int `json:"filmsQueued"`
	// EpisodesWanted: missing episodes Overtide searches for; Following:
	// series whose new episodes it fetches; Monitored: episodes airing now,
	// monitored for Sonarr's RSS.
	EpisodesWanted int `json:"episodesWanted"`
	// Seasons: seasons waiting for, or getting, a whole-season pack.
	Seasons   int    `json:"seasons"`
	Following int    `json:"following"`
	Monitored int    `json:"monitored"`
	Error     string `json:"error,omitempty"`
}

// Summary returns what the last "Manage all" pass did.
func (service *Service) Summary() (ManageSummary, error) {
	var summary ManageSummary
	raw, err := service.inv.Store().Meta(summaryKey)
	if err != nil || raw == "" {
		return summary, err
	}
	return summary, json.Unmarshal([]byte(raw), &summary)
}

func (service *Service) saveSummary(summary ManageSummary) error {
	encoded, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	return service.inv.Store().SetMeta(summaryKey, string(encoded))
}

// retag replaces, once, the former tags with adding's: Overswell's each
// with its own ("overswell-added" with "overtide-added", and so on), and
// the oldest, "overtide", with where the title came from: TagRequested or
// TagAdded for the titles in the waiting list, TagFound for the others.
func (service *Service) retag(ctx context.Context, cfg config.Config, entries []Entry) error {
	if done, err := service.inv.Store().Meta(tagsKey); err != nil || done != "" {
		return err
	}
	own := map[string]Source{}
	for _, entry := range entries {
		if entry.ArrID != 0 {
			own[fmt.Sprintf("%s|%s|%d", entry.ServiceID, entry.Kind, entry.ArrID)] = entry.Source
		}
	}
	labelFor := func(serviceID string, kind model.MediaType, id int) string {
		if label := tagFor(own[fmt.Sprintf("%s|%s|%d", serviceID, kind, id)]); label != "" {
			return label
		}
		return model.TagFound
	}
	type editor struct {
		labels    func() (map[int]string, error)
		titles    func() (map[int][]int, error)
		ensureTag func(string) (int, error)
		editTags  func(ids []int, tagID int, add bool) error
	}
	var editors []struct {
		serviceID string
		kind      model.MediaType
		editor
	}
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			return err
		}
		client = client.WithContext(ctx)
		editors = append(editors, struct {
			serviceID string
			kind      model.MediaType
			editor
		}{svc.ID, model.Movie, editor{client.TagLabels, func() (map[int][]int, error) {
			titles, err := client.Titles()
			tags := map[int][]int{}
			for _, t := range titles {
				tags[t.ID] = t.Tags
			}
			return tags, err
		}, client.EnsureTag, client.EditTags}})
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return err
		}
		client = client.WithContext(ctx)
		editors = append(editors, struct {
			serviceID string
			kind      model.MediaType
			editor
		}{svc.ID, model.Series, editor{client.TagLabels, func() (map[int][]int, error) {
			titles, err := client.Titles()
			tags := map[int][]int{}
			for _, t := range titles {
				tags[t.ID] = t.Tags
			}
			return tags, err
		}, client.EnsureTag, client.EditTags}})
	}
	for _, e := range editors {
		labels, err := e.labels()
		if err != nil {
			return fmt.Errorf("replace the former tags: %w", err)
		}
		// legacy is each of Overtide's former tags there, by ID, with what
		// replaces it ("" for the first one, by where the title came from).
		legacy := map[int]string{}
		for id, label := range labels {
			for _, old := range model.LegacyTags {
				if strings.EqualFold(label, old.Old) {
					legacy[id] = old.New
				}
			}
		}
		if len(legacy) == 0 {
			continue
		}
		titles, err := e.titles()
		if err != nil {
			return fmt.Errorf("replace the former tags: %w", err)
		}
		byLabel := map[string][]int{}
		byOld := map[int][]int{}
		for id, tags := range titles {
			for _, tag := range tags {
				replacement, old := legacy[tag]
				if !old {
					continue
				}
				if replacement == "" {
					replacement = labelFor(e.serviceID, e.kind, id)
				}
				byLabel[replacement] = append(byLabel[replacement], id)
				byOld[tag] = append(byOld[tag], id)
			}
		}
		for label, ids := range byLabel {
			tagID, err := e.ensureTag(label)
			if err != nil {
				return fmt.Errorf("replace the former tags: %w", err)
			}
			sort.Ints(ids)
			if err := e.editTags(ids, tagID, true); err != nil {
				return fmt.Errorf("replace the former tags: %w", err)
			}
		}
		for tag, ids := range byOld {
			sort.Ints(ids)
			if err := e.editTags(ids, tag, false); err != nil {
				return fmt.Errorf("replace the former tags: %w", err)
			}
		}
	}
	return service.inv.Store().SetMeta(tagsKey, service.now().UTC().Format(time.RFC3339))
}

// takeAll is "Manage all": every title in Radarr and Sonarr gets one of
// adding's tags, and Radarr and Sonarr no longer fetch anything on their
// own. What they would have fetched, Overtide fetches instead, at its own
// pace and without upgrades: a monitored film without its file goes in
// line; Sonarr's series are the Sonarr driver's (sonarrdriver.Driver.Take).
// What was unmonitored stays unwanted: a title Overtide removed, a season
// nobody wanted. In dry run, nothing changes: the summary says what would.
func (service *Service) takeAll(ctx context.Context, cfg config.Config, entries []Entry, dryRun bool) ([]Entry, error) {
	now := service.now()
	summary := ManageSummary{At: now, DryRun: dryRun}
	switchedOn := time.Time{}
	if raw, err := service.inv.Store().Meta(manageAllKey); err != nil {
		return entries, err
	} else if raw != "" {
		switchedOn, _ = time.Parse(time.RFC3339, raw)
	} else if !dryRun {
		if err := service.inv.Store().SetMeta(manageAllKey, now.UTC().Format(time.RFC3339)); err != nil {
			return entries, err
		}
	}
	labelFor := func(added time.Time) string {
		if switchedOn.IsZero() || !added.After(switchedOn) {
			return model.TagFound
		}
		return model.TagNew
	}
	active := activeEntries(entries)
	listed := map[string]bool{}
	for _, entry := range entries {
		if entry.ArrID != 0 {
			listed[fmt.Sprintf("%s|%s|%d", entry.ServiceID, entry.Kind, entry.ArrID)] = true
		}
	}
	fail := func(err error) ([]Entry, error) {
		summary.Error = err.Error()
		_ = service.saveSummary(summary)
		return entries, err
	}

	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			return fail(err)
		}
		client = client.WithContext(ctx)
		labels, err := client.TagLabels()
		if err != nil {
			return fail(err)
		}
		titles, err := client.Titles()
		if err != nil {
			return fail(err)
		}
		toTag := map[string][]int{}
		var toUnmonitor []int
		for _, t := range titles {
			key := fmt.Sprintf("%s|%s|%d", svc.ID, model.Movie, t.ID)
			if model.HasLabel(t.Tags, labels, model.TagIgnore) {
				continue
			}
			if !model.HasManagedLabel(t.Tags, labels) {
				label := labelFor(t.Added)
				toTag[label] = append(toTag[label], t.ID)
			}
			if !t.Monitored || active[key] {
				continue
			}
			toUnmonitor = append(toUnmonitor, t.ID)
			if !t.HasFile && !listed[key] && t.TMDBID > 0 {
				// Radarr's lists add nothing (see lists.go): a film added
				// since "Manage all" was added by hand, and goes first.
				source, result := FromLibrary, "Taken from Radarr"
				if labelFor(t.Added) == model.TagNew {
					source, result = FromHand, "Added by hand in Radarr"
				}
				entries = append(entries, Entry{Key: movieKey(t.TMDBID), Kind: model.Movie, ServiceID: svc.ID, ExternalID: t.TMDBID,
					Title: t.Title, Year: t.Year, Source: source, Position: len(entries), State: Waiting, ArrID: t.ID,
					NextTry: now, AddedAt: now, UpdatedAt: now, Result: result})
				listed[key] = true
				summary.FilmsQueued++
			}
		}
		for label, ids := range toTag {
			summary.Tagged += len(ids)
			if dryRun {
				continue
			}
			tagID, err := client.EnsureTag(label)
			if err != nil {
				return fail(err)
			}
			if err := client.EditTags(ids, tagID, true); err != nil {
				return fail(err)
			}
		}
		summary.Unmonitored += len(toUnmonitor)
		if !dryRun {
			if err := client.SetManyMonitored(toUnmonitor, false); err != nil {
				return fail(err)
			}
		}
	}

	taken, err := service.sonarr.Take(ctx, cfg, active, labelFor, dryRun)
	summary.Tagged += taken.Tagged
	summary.SeriesTaken += taken.SeriesTaken
	summary.EpisodesWanted += taken.EpisodesWanted
	summary.Following += taken.Following
	summary.Monitored += taken.Monitored
	if err != nil {
		return fail(err)
	}
	return entries, service.saveSummary(summary)
}

// activeEntries are the titles Overtide monitors itself, for a release:
// left as they are.
func activeEntries(entries []Entry) map[string]bool {
	active := map[string]bool{}
	for _, entry := range entries {
		if entry.ArrID != 0 && entry.State == NotReleased && !entry.MonitoredAt.IsZero() {
			active[fmt.Sprintf("%s|%s|%d", entry.ServiceID, entry.Kind, entry.ArrID)] = true
		}
	}
	return active
}

// replaceSeasons has the Sonarr driver replace seasons with whole-season
// packs (sonarrdriver.Driver.ReplaceSeasons), and keeps in the summary how
// many seasons wait for theirs. It returns the searches asked for.
func (service *Service) replaceSeasons(ctx context.Context, cfg config.Config, budget int, dryRun, fresh bool) (int, error) {
	used, waiting, err := service.sonarr.ReplaceSeasons(ctx, cfg, budget, dryRun, fresh)
	if err != nil {
		return used, err
	}
	if summary, err := service.Summary(); err == nil && !summary.At.IsZero() {
		summary.Seasons = waiting
		_ = service.saveSummary(summary)
	}
	return used, nil
}
