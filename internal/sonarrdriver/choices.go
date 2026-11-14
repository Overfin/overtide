package sonarrdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"sort"
	"time"

	"github.com/overfin/overtide/internal/services/sonarr"
)

// importChoicesKey is where the operator's choices wait for the next run. A
// run holds the imports' state for minutes, so they don't touch it directly.
const importChoicesKey = "import_choices"

// What the operator may do with a download whose files wait.
const (
	// ChoiceEpisodes: these files are these episodes.
	ChoiceEpisodes = "episodes"
	// ChoiceAnother: leave this release, try another.
	ChoiceAnother = "another"
	// ChoiceIgnore: leave this release, and its season as it is.
	ChoiceIgnore = "ignore"
)

// ImportChoice is what the operator said of a download.
type ImportChoice struct {
	ServiceID  string `json:"serviceId"`
	DownloadID string `json:"downloadId"`
	Action     string `json:"action"`
	// Files gives, for ChoiceEpisodes, each file's episode IDs.
	Files map[string][]int `json:"files,omitempty"`
}

// ChooseImport does what the operator said of a download, now: files given
// their episodes are imported at once, and the download leaves the waiting
// list at once whatever the action (chosen). The choice is also kept for
// the next run, which records it (applyChoices): a run going on meanwhile
// holds the imports' state for minutes, and would write over a change made
// to it here.
func (driver *Driver) ChooseImport(ctx context.Context, choice ImportChoice) error {
	switch choice.Action {
	case ChoiceEpisodes:
		if len(choice.Files) == 0 {
			return fmt.Errorf("no file was given an episode")
		}
		imported, err := driver.importChosen(ctx, choice)
		if err != nil {
			return err
		}
		// Only those: a file given no episode of the series still waits.
		kept := map[string][]int{}
		for path, ids := range choice.Files {
			if imported[path] {
				kept[path] = ids
			}
		}
		choice.Files = kept
	case ChoiceAnother, ChoiceIgnore:
	default:
		return fmt.Errorf("unknown action")
	}
	driver.choicesMu.Lock()
	defer driver.choicesMu.Unlock()
	choices, err := driver.readChoices()
	if err != nil {
		return err
	}
	return driver.writeChoices(append(choices, choice))
}

// importChosen imports the files of a download the operator gave their
// episodes, now. The run that records the choice finds them imported, and
// leaves them. It returns the files imported, by path.
func (driver *Driver) importChosen(ctx context.Context, choice ImportChoice) (map[string]bool, error) {
	states, err := driver.States()
	if err != nil {
		return nil, err
	}
	state := states[choice.ServiceID]
	if state == nil || state.Imports[choice.DownloadID] == nil || state.Imports[choice.DownloadID].Done {
		return nil, fmt.Errorf("that download isn't waiting")
	}
	download := state.Imports[choice.DownloadID]
	client, err := driver.inv.SonarrClient(choice.ServiceID)
	if err != nil {
		return nil, err
	}
	client = client.WithContext(ctx)
	files, err := client.DownloadFiles(choice.DownloadID)
	if err != nil {
		return nil, err
	}
	episodes, err := client.Episodes(download.SeriesID)
	if err != nil {
		return nil, err
	}
	items, _ := operatorSays(files, nil, nil, choice.Files, episodes)
	if len(items) == 0 {
		return nil, fmt.Errorf("none of these files is still there, or none was given an episode of the series")
	}
	imported := map[string]bool{}
	for i := range items {
		items[i].DownloadID = choice.DownloadID
		imported[items[i].Path] = true
	}
	if _, err := client.Import(download.SeriesID, items); err != nil {
		return nil, err
	}
	log.Printf("[adding] imported %d files of %s, as the operator said", len(items), download.Title)
	return imported, nil
}

func (driver *Driver) readChoices() ([]ImportChoice, error) {
	raw, err := driver.inv.Store().Meta(importChoicesKey)
	if err != nil || raw == "" {
		return nil, err
	}
	var choices []ImportChoice
	return choices, json.Unmarshal([]byte(raw), &choices)
}

func (driver *Driver) writeChoices(choices []ImportChoice) error {
	encoded, err := json.Marshal(choices)
	if err != nil {
		return err
	}
	return driver.inv.Store().SetMeta(importChoicesKey, string(encoded))
}

// applyingChoice is a choice a run took, and when.
type applyingChoice struct {
	choice ImportChoice
	at     time.Time
}

// applyingFor is how long a choice taken shows as chosen without a save: a
// run that failed before saving leaves it, and the files show as waiting
// again.
const applyingFor = 30 * time.Minute

func choiceKey(serviceID, downloadID string) string { return serviceID + "/" + downloadID }

// chosen is what the operator said of each download that the state
// doesn't show yet: the choices kept for a run, and those one took and
// hasn't saved. They count at once wherever the waiting files are read, so
// what the operator did shows when they do it, whatever a run is doing.
func (driver *Driver) chosen() map[string][]ImportChoice {
	driver.choicesMu.Lock()
	defer driver.choicesMu.Unlock()
	out := map[string][]ImportChoice{}
	now := driver.now()
	for key, a := range driver.applying {
		if now.Sub(a.at) < applyingFor {
			out[key] = append(out[key], a.choice)
		}
	}
	choices, err := driver.readChoices()
	if err != nil {
		log.Printf("[adding] the operator's choices: %v", err)
	}
	for _, choice := range choices {
		key := choiceKey(choice.ServiceID, choice.DownloadID)
		out[key] = append(out[key], choice)
	}
	return out
}

// takeChoices returns a Sonarr's choices, and leaves the others.
func (driver *Driver) takeChoices(serviceID string) []ImportChoice {
	driver.choicesMu.Lock()
	defer driver.choicesMu.Unlock()
	choices, err := driver.readChoices()
	if err != nil {
		log.Printf("[adding] the operator's choices: %v", err)
		return nil
	}
	var mine, others []ImportChoice
	for _, choice := range choices {
		if choice.ServiceID == serviceID {
			mine = append(mine, choice)
		} else {
			others = append(others, choice)
		}
	}
	if len(mine) > 0 {
		if err := driver.writeChoices(others); err != nil {
			log.Printf("[adding] the operator's choices: %v", err)
			return nil
		}
		if driver.applying == nil {
			driver.applying = map[string]applyingChoice{}
		}
		for _, choice := range mine {
			driver.applying[choiceKey(choice.ServiceID, choice.DownloadID)] = applyingChoice{choice, driver.now()}
		}
	}
	return mine
}

// applyChoices does what the operator said of a Sonarr's downloads.
func (driver *Driver) applyChoices(client *sonarr.Client, state *State, serviceID string) {
	for _, choice := range driver.takeChoices(serviceID) {
		download := state.Imports[choice.DownloadID]
		if download == nil || download.Done {
			continue
		}
		switch choice.Action {
		case ChoiceEpisodes:
			if download.Chosen == nil {
				download.Chosen = map[string][]int{}
			}
			for file, episodes := range choice.Files {
				download.Chosen[file] = episodes
			}
		case ChoiceAnother:
			driver.tryAnother(client, state, download)
		case ChoiceIgnore:
			for _, pack := range state.Seasons {
				if pack.Release == download.Title || pack.DownloadID == choice.DownloadID {
					pack.Done = true
				}
			}
			download.Done, download.Waiting = true, nil
		}
	}
}

// tryAnother leaves a release: its season is searched again for another, at
// once. Its files are left where they are, in the download client.
func (driver *Driver) tryAnother(client *sonarr.Client, state *State, download *downloadImport) {
	seasons := map[int]bool{}
	for _, file := range download.Waiting {
		if season := fileSeason(file.Path); season > 0 {
			seasons[season] = true
		}
	}
	download.Done, download.Waiting = true, nil
	episodes, err := client.Episodes(download.SeriesID)
	if err != nil {
		log.Printf("[adding] the episodes of %s: %v", download.Title, err)
	}
	now := driver.now()
	for season := range seasons {
		key := seasonKey(download.SeriesID, season)
		pack := state.Seasons[key]
		if pack == nil {
			pack = &SeasonPack{SeriesID: download.SeriesID, Season: season}
			state.Seasons[key] = pack
		}
		if !slices.Contains(pack.Tried, download.Title) {
			pack.Tried = append(pack.Tried, download.Title)
		}
		pack.Release, pack.GrabbedAt, pack.DownloadID, pack.Done, pack.NextTry = "", time.Time{}, "", false, now
		// A season is only searched for what it wants: all it lacks.
		for _, e := range episodes {
			if e.SeasonNumber == season && !e.HasFile {
				if _, wanted := state.Wanted[e.ID]; !wanted {
					state.Wanted[e.ID] = EpisodeWant{SeriesID: download.SeriesID, Season: season}
				}
			}
		}
	}
}

// settledSeasons are the seasons whose episodes all have a file that came
// after since: a download's older files for them have no more use.
func settledSeasons(client *sonarr.Client, seriesID int, episodes []sonarr.Episode, since time.Time) (map[int]bool, error) {
	files, err := client.EpisodeFiles(seriesID)
	if err != nil {
		return nil, err
	}
	added := map[int]time.Time{}
	for _, f := range files {
		added[f.ID] = f.DateAdded
	}
	settled := map[int]bool{}
	for _, e := range episodes {
		if _, seen := settled[e.SeasonNumber]; !seen {
			settled[e.SeasonNumber] = true
		}
		if e.EpisodeFileID == 0 || !added[e.EpisodeFileID].After(since) {
			settled[e.SeasonNumber] = false
		}
	}
	return settled, nil
}

// WaitingImport is a download with files waiting, for the Adding page.
type WaitingImport struct {
	ServiceID  string        `json:"serviceId"`
	DownloadID string        `json:"downloadId"`
	Series     string        `json:"series"`
	Release    string        `json:"release"`
	GrabbedAt  time.Time     `json:"grabbedAt"`
	Files      []waitingFile `json:"files"`
}

// WaitingImports lists the downloads whose files wait, the latest first.
func (driver *Driver) WaitingImports() ([]WaitingImport, error) {
	states, err := driver.States()
	if err != nil {
		return nil, err
	}
	chosen := driver.chosen()
	out := []WaitingImport{}
	for serviceID, state := range states {
		names := driver.seriesNames(serviceID)
		for id, download := range state.Imports {
			if download.Done || len(download.Waiting) == 0 {
				continue
			}
			files := slices.Clone(download.Waiting)
			// What the operator said counts already: another release or
			// none, it's gone; files given episodes are imported.
			gone := false
			for _, choice := range chosen[choiceKey(serviceID, id)] {
				gone = gone || choice.Action != ChoiceEpisodes
				files = slices.DeleteFunc(files, func(file waitingFile) bool {
					_, said := choice.Files[file.Path]
					return said
				})
			}
			if gone || len(files) == 0 {
				continue
			}
			sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
			out = append(out, WaitingImport{ServiceID: serviceID, DownloadID: id, Series: names[download.SeriesID], Release: download.Title, GrabbedAt: download.GrabbedAt, Files: files})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].GrabbedAt.Equal(out[j].GrabbedAt) {
			return out[i].GrabbedAt.After(out[j].GrabbedAt)
		}
		return out[i].DownloadID < out[j].DownloadID
	})
	return out, nil
}

// EpisodeOption is an episode a waiting file may be said to be.
type EpisodeOption struct {
	ID      int    `json:"id"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Title   string `json:"title"`
}

// ImportOptions gives what the operator chooses from for a download: its
// series' episodes, and what Sonarr makes of each file's number.
func (driver *Driver) ImportOptions(ctx context.Context, serviceID, downloadID string) ([]EpisodeOption, map[string][]int, error) {
	states, err := driver.States()
	if err != nil {
		return nil, nil, err
	}
	state := states[serviceID]
	if state == nil || state.Imports[downloadID] == nil {
		return nil, nil, fmt.Errorf("that download isn't known")
	}
	client, err := driver.inv.SonarrClient(serviceID)
	if err != nil {
		return nil, nil, err
	}
	client = client.WithContext(ctx)
	episodes, err := client.Episodes(state.Imports[downloadID].SeriesID)
	if err != nil {
		return nil, nil, err
	}
	options := make([]EpisodeOption, 0, len(episodes))
	for _, e := range episodes {
		options = append(options, EpisodeOption{ID: e.ID, Season: e.SeasonNumber, Episode: e.EpisodeNumber, Title: e.Title})
	}
	sort.Slice(options, func(i, j int) bool {
		if options[i].Season != options[j].Season {
			return options[i].Season < options[j].Season
		}
		return options[i].Episode < options[j].Episode
	})
	guesses := map[string][]int{}
	files, err := client.DownloadFiles(downloadID)
	if err == nil {
		for _, file := range files {
			for _, e := range file.Episodes {
				guesses[file.Path] = append(guesses[file.Path], e.ID)
			}
		}
	}
	return options, guesses, nil
}
