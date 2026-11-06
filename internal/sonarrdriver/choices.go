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

// ChooseImport notes the operator's choice: the next run applies it.
func (driver *Driver) ChooseImport(choice ImportChoice) error {
	switch choice.Action {
	case ChoiceEpisodes:
		if len(choice.Files) == 0 {
			return fmt.Errorf("no file was given an episode")
		}
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
	out := []WaitingImport{}
	for serviceID, state := range states {
		names := driver.seriesNames(serviceID)
		for id, download := range state.Imports {
			if download.Done || len(download.Waiting) == 0 {
				continue
			}
			files := slices.Clone(download.Waiting)
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
