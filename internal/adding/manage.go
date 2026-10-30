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
	"github.com/overfin/overtide/internal/services/sonarr"
)

const (
	// manageAllKey records when "Manage all" first took the libraries: a
	// title added after it is TagNew, one there before TagFound.
	manageAllKey = "adding.manage_all_at"
	// episodesKey keeps, per Sonarr, the series whose new episodes are
	// wanted and the missing episodes to search for.
	episodesKey = "adding.episodes"
	// summaryKey keeps what the last "Manage all" pass did, or would do.
	summaryKey = "adding.manage_summary"
)

// missingPages bounds how many pages of Sonarr's missing episodes a pass
// reads when it takes series (200 each).
const missingPages = 50

// ManageSummary is what the last "Manage all" pass did, or would do in dry
// run: shown on the Adding page.
type ManageSummary struct {
	At     time.Time `json:"at"`
	DryRun bool      `json:"dryRun"`
	// Tagged: titles that got one of Overtide's tags; Unmonitored: films
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

// episodeWant is a missing episode Overtide searches for.
type episodeWant struct {
	SeriesID int       `json:"seriesId"`
	Season   int       `json:"season"`
	Tries    int       `json:"tries,omitempty"`
	NextTry  time.Time `json:"nextTry,omitempty"`
	// Grabbed: its search started a download, followed until imported.
	Grabbed bool `json:"grabbed,omitempty"`
}

// sonarrState is what "Manage all" keeps for one Sonarr.
type sonarrState struct {
	// Follow: series whose new episodes are wanted, as Sonarr would have
	// fetched them (followed series, or with their last season monitored).
	Follow map[int]bool `json:"follow"`
	// Wanted: missing episodes that were monitored when Overtide took
	// their series, or that aired without coming through the RSS.
	Wanted map[int]episodeWant `json:"wanted"`
	// Seasons: seasons replaced, or to be, by a whole-season pack, by
	// seasonKey.
	Seasons map[string]*seasonPack `json:"seasons,omitempty"`
}

func (service *Service) episodeStates() (map[string]*sonarrState, error) {
	states := map[string]*sonarrState{}
	raw, err := service.inv.Store().Meta(episodesKey)
	if err != nil || raw == "" {
		return states, err
	}
	if err := json.Unmarshal([]byte(raw), &states); err != nil {
		return nil, fmt.Errorf("read the wanted episodes: %w", err)
	}
	return states, nil
}

func (service *Service) saveEpisodeStates(states map[string]*sonarrState) error {
	encoded, err := json.Marshal(states)
	if err != nil {
		return err
	}
	return service.inv.Store().SetMeta(episodesKey, string(encoded))
}

func stateFor(states map[string]*sonarrState, serviceID string) *sonarrState {
	state := states[serviceID]
	if state == nil {
		state = &sonarrState{}
		states[serviceID] = state
	}
	if state.Follow == nil {
		state.Follow = map[int]bool{}
	}
	if state.Wanted == nil {
		state.Wanted = map[int]episodeWant{}
	}
	if state.Seasons == nil {
		state.Seasons = map[string]*seasonPack{}
	}
	return state
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

// retag replaces, once, the legacy tag every managed title had with the
// tags that say where it came from: TagRequested or TagAdded for the
// titles Overtide added (its waiting list has them), TagFound for the
// others.
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
			return fmt.Errorf("replace the overtide tag: %w", err)
		}
		legacy := 0
		for id, label := range labels {
			if strings.EqualFold(label, model.LegacyTag) {
				legacy = id
			}
		}
		if legacy == 0 {
			continue
		}
		titles, err := e.titles()
		if err != nil {
			return fmt.Errorf("replace the overtide tag: %w", err)
		}
		byLabel := map[string][]int{}
		var all []int
		for id, tags := range titles {
			for _, tag := range tags {
				if tag == legacy {
					label := labelFor(e.serviceID, e.kind, id)
					byLabel[label] = append(byLabel[label], id)
					all = append(all, id)
				}
			}
		}
		for label, ids := range byLabel {
			tagID, err := e.ensureTag(label)
			if err != nil {
				return fmt.Errorf("replace the overtide tag: %w", err)
			}
			sort.Ints(ids)
			if err := e.editTags(ids, tagID, true); err != nil {
				return fmt.Errorf("replace the overtide tag: %w", err)
			}
		}
		sort.Ints(all)
		if err := e.editTags(all, legacy, false); err != nil {
			return fmt.Errorf("replace the overtide tag: %w", err)
		}
	}
	return service.inv.Store().SetMeta(tagsKey, service.now().UTC().Format(time.RFC3339))
}

// takeAll is "Manage all": every title in Radarr and Sonarr gets one of
// Overtide's tags, and Radarr and Sonarr no longer fetch anything on their
// own. What they would have fetched, Overtide fetches instead, at its own
// pace and without upgrades: a monitored film without its file goes in
// line; a monitored missing episode goes in the wanted episodes; a series
// whose new episodes were followed keeps them coming, each monitored from
// ReleaseWindow before it airs until it's downloaded (Sonarr's RSS grabs
// it), then searched for by Overtide after ReleaseGrace. What was
// unmonitored stays unwanted: a title Overtide removed, a season nobody
// wanted. In dry run, nothing changes: the summary says what would.
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
	// What Overtide monitors itself, for a release: left as it is.
	active := map[string]bool{}
	listed := map[string]bool{}
	for _, entry := range entries {
		if entry.ArrID != 0 {
			key := fmt.Sprintf("%s|%s|%d", entry.ServiceID, entry.Kind, entry.ArrID)
			listed[key] = true
			if entry.State == NotReleased && !entry.MonitoredAt.IsZero() {
				active[key] = true
			}
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
			if hasLabel(t.Tags, labels, model.TagIgnore) {
				continue
			}
			if !hasManagedTag(t.Tags, labels) {
				label := labelFor(t.Added)
				toTag[label] = append(toTag[label], t.ID)
			}
			if !t.Monitored || active[key] {
				continue
			}
			toUnmonitor = append(toUnmonitor, t.ID)
			if !t.HasFile && !listed[key] && t.TMDBID > 0 {
				entries = append(entries, Entry{Key: movieKey(t.TMDBID), Kind: model.Movie, ServiceID: svc.ID, ExternalID: t.TMDBID,
					Title: t.Title, Year: t.Year, Source: FromLibrary, Position: len(entries), State: Waiting, ArrID: t.ID,
					NextTry: now, AddedAt: now, UpdatedAt: now, Result: "Taken from Radarr"})
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

	states, err := service.episodeStates()
	if err != nil {
		return fail(err)
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return fail(err)
		}
		client = client.WithContext(ctx)
		state := stateFor(states, svc.ID)
		labels, err := client.TagLabels()
		if err != nil {
			return fail(err)
		}
		titles, err := client.Titles()
		if err != nil {
			return fail(err)
		}
		toTag := map[string][]int{}
		var toTake []sonarr.Title
		for _, t := range titles {
			if hasLabel(t.Tags, labels, model.TagIgnore) {
				// Left alone: nothing of it is followed or searched.
				delete(state.Follow, t.ID)
				for id, want := range state.Wanted {
					if want.SeriesID == t.ID {
						delete(state.Wanted, id)
					}
				}
				continue
			}
			if !hasManagedTag(t.Tags, labels) {
				label := labelFor(t.Added)
				toTag[label] = append(toTag[label], t.ID)
			}
			if !t.Taken() && !active[fmt.Sprintf("%s|%s|%d", svc.ID, model.Series, t.ID)] {
				toTake = append(toTake, t)
			}
		}
		if len(toTake) > 0 {
			// What Sonarr would have fetched for them: its monitored missing
			// episodes, read before they're unmonitored.
			taking := map[int]bool{}
			for _, t := range toTake {
				taking[t.ID] = true
				state.Follow[t.ID] = followsNew(t)
			}
			for page := 1; page <= missingPages; page++ {
				missing, total, err := client.MissingEpisodes(true, page, 200)
				if err != nil {
					return fail(err)
				}
				for _, e := range missing {
					// A recent one in a followed series is the airing
					// window's: monitored for the RSS until ReleaseGrace.
					recent := e.AirDateUTC.After(now.Add(-ReleaseGrace))
					if taking[e.SeriesID] && !e.AirDateUTC.IsZero() && !e.AirDateUTC.After(now) && !(recent && state.Follow[e.SeriesID]) {
						if _, there := state.Wanted[e.ID]; !there {
							state.Wanted[e.ID] = episodeWant{SeriesID: e.SeriesID, Season: e.SeasonNumber}
						}
					}
				}
				if len(missing) == 0 || page*200 >= total {
					break
				}
			}
			summary.SeriesTaken += len(toTake)
			if !dryRun {
				for _, t := range toTake {
					if err := client.TakeSeries(t.ID); err != nil {
						return fail(err)
					}
				}
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
		monitored, err := service.airingWindow(client, state, active, svc.ID, dryRun)
		if err != nil {
			return fail(err)
		}
		summary.Monitored += monitored
		for _, follow := range state.Follow {
			if follow {
				summary.Following++
			}
		}
		summary.EpisodesWanted += len(state.Wanted)
	}
	if dryRun {
		service.preview = states
	} else if err := service.saveEpisodeStates(states); err != nil {
		return fail(err)
	}
	return entries, service.saveSummary(summary)
}

// followsNew reports whether Sonarr would have fetched the series' new
// episodes: it's monitored, and new seasons are, or its last season is.
func followsNew(t sonarr.Title) bool {
	if !t.Monitored {
		return false
	}
	if t.MonitorNewItems == "all" {
		return true
	}
	last, monitored := -1, false
	for _, season := range t.Seasons {
		if season.SeasonNumber > last {
			last, monitored = season.SeasonNumber, season.Monitored
		}
	}
	return monitored
}

// airingWindow monitors, for the followed series, each episode airing
// within ReleaseWindow without its file, until ReleaseGrace after it aired
// or its download is confirmed, so Sonarr's RSS grabs it; and unmonitors
// the others. One that aired ReleaseGrace ago without coming goes in the
// wanted episodes. It returns how many episodes are monitored.
func (service *Service) airingWindow(client *sonarr.Client, state *sonarrState, active map[string]bool, serviceID string, dryRun bool) (int, error) {
	now := service.now()
	episodes, err := client.Calendar(now.Add(-ReleaseGrace-2*24*time.Hour), now.Add(ReleaseWindow))
	if err != nil {
		return 0, err
	}
	queued, err := client.QueuedEpisodes()
	if err != nil {
		return 0, err
	}
	var on, off []int
	monitored := 0
	for _, e := range episodes {
		if !state.Follow[e.SeriesID] || active[fmt.Sprintf("%s|%s|%d", serviceID, model.Series, e.SeriesID)] {
			continue
		}
		inWindow := !e.AirDateUTC.IsZero() && !e.AirDateUTC.After(now.Add(ReleaseWindow)) && !e.AirDateUTC.Before(now.Add(-ReleaseGrace))
		want := inWindow && !e.HasFile && !queued[e.ID]
		if want {
			monitored++
		}
		switch {
		case want && !e.Monitored:
			on = append(on, e.ID)
		case !want && e.Monitored:
			off = append(off, e.ID)
		}
		if !e.HasFile && !queued[e.ID] && !e.AirDateUTC.IsZero() && e.AirDateUTC.Before(now.Add(-ReleaseGrace)) {
			if _, there := state.Wanted[e.ID]; !there {
				state.Wanted[e.ID] = episodeWant{SeriesID: e.SeriesID, Season: e.SeasonNumber}
			}
		}
	}
	if dryRun {
		return monitored, nil
	}
	if err := client.SetEpisodesMonitored(on, true); err != nil {
		return 0, err
	}
	return monitored, client.SetEpisodesMonitored(off, false)
}

// hasLabel reports whether one of the tag IDs is the tag.
func hasLabel(ids []int, labels map[int]string, tag string) bool {
	for _, id := range ids {
		if strings.EqualFold(labels[id], tag) {
			return true
		}
	}
	return false
}

func hasManagedTag(ids []int, labels map[int]string) bool {
	for _, id := range ids {
		if model.IsManagedTag(labels[id]) {
			return true
		}
	}
	return false
}

// searchEpisodes searches for the wanted episodes whose turn it is, one at
// a time, at most budget of them: a download started, it's followed until
// imported; nothing found, it's tried again later (retryDelay). An episode
// that has its file, or left Sonarr, leaves the list.
func (service *Service) searchEpisodes(ctx context.Context, cfg config.Config, budget int, dryRun bool) error {
	states, err := service.episodeStates()
	if err != nil {
		return err
	}
	now := service.now()
	for _, svc := range cfg.ServicesOfType("sonarr") {
		state := states[svc.ID]
		if state == nil || len(state.Wanted) == 0 {
			continue
		}
		state = stateFor(states, svc.ID)
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return err
		}
		client = client.WithContext(ctx)
		queued, err := client.QueuedEpisodes()
		if err != nil {
			return err
		}
		// Downloads started: imported, or back in line if they stopped.
		for id, want := range state.Wanted {
			if !want.Grabbed || queued[id] {
				continue
			}
			episode, err := client.GetEpisode(id)
			switch {
			case sonarr.IsNotFound(err):
				delete(state.Wanted, id)
			case err != nil:
				continue
			case episode.HasFile:
				delete(state.Wanted, id)
			default:
				want.Grabbed = false
				want.Tries++
				want.NextTry = now.Add(retryDelay(want.Tries))
				state.Wanted[id] = want
			}
		}
		if dryRun || budget <= 0 {
			continue
		}
		var due []int
		for id, want := range state.Wanted {
			if state.Seasons[seasonKey(want.SeriesID, want.Season)].pending() {
				continue // its season comes whole
			}
			if !want.Grabbed && !want.NextTry.After(now) {
				due = append(due, id)
			}
		}
		// The ones never tried, then the longest waiting.
		sort.Slice(due, func(i, j int) bool {
			a, b := state.Wanted[due[i]], state.Wanted[due[j]]
			if !a.NextTry.Equal(b.NextTry) {
				return a.NextTry.Before(b.NextTry)
			}
			return due[i] > due[j]
		})
		root, _, err := client.Destination()
		if err != nil {
			return err
		}
		disk := service.diskStop
		if service.disk != nil {
			disk = service.disk
		}
		if disk(cfg, root, false) != "" {
			continue
		}
		for _, id := range due {
			if budget <= 0 || ctx.Err() != nil {
				break
			}
			want := state.Wanted[id]
			episode, err := client.GetEpisode(id)
			if sonarr.IsNotFound(err) || (err == nil && episode.HasFile) {
				delete(state.Wanted, id)
				continue
			}
			if err != nil {
				continue
			}
			budget--
			grabbed, err := searchAndCheck(ctx,
				func() (int, error) { return client.SearchEpisodes([]int{id}) },
				client.CommandStatus,
				func() (bool, error) {
					queued, err := client.QueuedEpisodes()
					return queued[id], err
				})
			if err == nil && grabbed {
				want.Grabbed = true
			} else {
				want.Tries++
				want.NextTry = now.Add(retryDelay(want.Tries))
			}
			state.Wanted[id] = want
		}
	}
	return service.saveEpisodeStates(states)
}
