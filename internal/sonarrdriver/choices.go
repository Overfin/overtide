package sonarrdriver

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/videolength"
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
	files, err := driver.downloadFiles(ctx, client, choice.DownloadID)
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

// EpisodeOption is an episode a waiting file may be said to be: Sonarr's,
// with what TMDB says of it when TMDB's episode is known to be this one
// (Translated, in the server's language; Runtime, in minutes).
type EpisodeOption struct {
	ID         int    `json:"id"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	Title      string `json:"title"`
	Translated string `json:"translated,omitempty"`
	AirDate    string `json:"airDate,omitempty"`
	Runtime    int    `json:"runtime,omitempty"`
}

// Meaning is what a file's number means in one order: Sonarr's, TMDB's own,
// or one of TMDB's others (Order is its name). Nothing when the order has
// no such episode.
type Meaning struct {
	Order      string `json:"order"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	Title      string `json:"title,omitempty"`
	Translated string `json:"translated,omitempty"`
	AirDate    string `json:"airDate,omitempty"`
	Runtime    int    `json:"runtime,omitempty"`
	Missing    bool   `json:"missing,omitempty"`
}

// FileFacts is what helps tell a waiting file apart: how long it lasts
// (seconds, 0 when it can't be read) and what its number means in each
// order.
type FileFacts struct {
	Seconds  int       `json:"seconds,omitempty"`
	Meanings []Meaning `json:"meanings"`
}

// ImportChoices is what the operator chooses from for a download.
type ImportChoices struct {
	Episodes []EpisodeOption      `json:"episodes"`
	Guesses  map[string][]int     `json:"guesses"`
	Files    map[string]FileFacts `json:"files"`
}

// ImportOptions gives what the operator chooses from for a download: its
// series' episodes, what Sonarr makes of each file's number, and what helps
// tell the files apart (FileFacts).
func (driver *Driver) ImportOptions(ctx context.Context, serviceID, downloadID string) (*ImportChoices, error) {
	states, err := driver.States()
	if err != nil {
		return nil, err
	}
	state := states[serviceID]
	if state == nil || state.Imports[downloadID] == nil {
		return nil, fmt.Errorf("that download isn't known")
	}
	download := state.Imports[downloadID]
	client, err := driver.inv.SonarrClient(serviceID)
	if err != nil {
		return nil, err
	}
	client = client.WithContext(ctx)
	episodes, err := client.Episodes(download.SeriesID)
	if err != nil {
		return nil, err
	}
	// Asked now, not from a run's cache: a form is opened now and then.
	var orders *overfin.EpisodeOrders
	if tmdbID := driver.tmdbOf(client, download.SeriesID); tmdbID != 0 {
		if orders, err = driver.inv.OverfinClient().WithContext(ctx).EpisodeOrders(tmdbID); err != nil {
			log.Printf("[adding] what Overfin says of series %d: %v", tmdbID, err)
			orders = nil
		}
	}
	tmdbOf := tmdbEpisodes(episodes, orders, driver.now())
	out := &ImportChoices{Episodes: make([]EpisodeOption, 0, len(episodes)), Guesses: map[string][]int{}, Files: map[string]FileFacts{}}
	for _, e := range episodes {
		option := EpisodeOption{ID: e.ID, Season: e.SeasonNumber, Episode: e.EpisodeNumber, Title: e.Title, AirDate: e.AirDate}
		if t, ok := tmdbOf[e.ID]; ok {
			option.Translated, option.Runtime = t.Translated, t.Runtime
		}
		out.Episodes = append(out.Episodes, option)
	}
	sort.Slice(out.Episodes, func(i, j int) bool {
		if out.Episodes[i].Season != out.Episodes[j].Season {
			return out.Episodes[i].Season < out.Episodes[j].Season
		}
		return out.Episodes[i].Episode < out.Episodes[j].Episode
	})
	files, err := driver.downloadFiles(ctx, client, downloadID)
	if err != nil {
		return out, nil // the episodes are still worth choosing from
	}
	for _, file := range files {
		for _, e := range file.Episodes {
			out.Guesses[file.Path] = append(out.Guesses[file.Path], e.ID)
		}
		facts := FileFacts{Meanings: meanings(file.Path, episodes, tmdbOf, orders)}
		if length, err := videolength.Of(file.Path); err == nil {
			facts.Seconds = int(length.Seconds())
		}
		out.Files[file.Path] = facts
	}
	return out, nil
}

// tmdbEpisodes is, by Sonarr episode, TMDB's own episode known to be the
// same: by their English title, else by number in a season whose numbers
// can be believed, else by an air date only one episode of each has.
func tmdbEpisodes(episodes []sonarr.Episode, orders *overfin.EpisodeOrders, now time.Time) map[int]overfin.OrderEpisode {
	out := map[int]overfin.OrderEpisode{}
	if orders == nil {
		return out
	}
	titles := titleIndex{}
	byNumber := map[[2]int]int{}
	byDate := map[string][]int{}
	for _, e := range episodes {
		titles.add(e.Title, e.ID)
		byNumber[[2]int{e.SeasonNumber, e.EpisodeNumber}] = e.ID
		if e.AirDate != "" {
			byDate[e.AirDate] = append(byDate[e.AirDate], e.ID)
		}
	}
	tmdbDates := map[string]int{}
	for _, season := range orders.Seasons {
		for _, e := range season.Episodes {
			if e.AirDate != "" {
				tmdbDates[e.AirDate]++
			}
		}
	}
	for _, season := range orders.Seasons {
		numbersHold := seasonCheck(season.Season, episodes, orders, now) == ""
		for _, e := range season.Episodes {
			var id int
			if same := titles[comparableTitle(e.Name)]; len(same) == 1 {
				id = same[0]
			} else if numbersHold {
				id = byNumber[[2]int{season.Season, e.Episode}]
			} else if ids := byDate[e.AirDate]; e.AirDate != "" && len(ids) == 1 && tmdbDates[e.AirDate] == 1 {
				id = ids[0]
			}
			if _, taken := out[id]; id != 0 && !taken {
				out[id] = e
			}
		}
	}
	return out
}

// fileNumber is the season and episode a file's name gives, ok false when
// it gives none.
func fileNumber(file string) (season, episode int, ok bool) {
	match := fileNumberMark.FindStringSubmatch(path.Base(strings.ReplaceAll(file, `\`, "/")))
	if match == nil {
		return 0, 0, false
	}
	season, _ = strconv.Atoi(match[1] + match[3])
	episode, _ = strconv.Atoi(match[2] + match[4])
	return season, episode, true
}

var fileNumberMark = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:s(\d{1,3})[ ._-]?e(\d{1,4})|(\d{1,3})x(\d{2,4}))`)

// meanings is what a file's number means in Sonarr's order, TMDB's own and
// each of TMDB's others.
func meanings(file string, episodes []sonarr.Episode, tmdbOf map[int]overfin.OrderEpisode, orders *overfin.EpisodeOrders) []Meaning {
	season, episode, ok := fileNumber(file)
	if !ok {
		return []Meaning{}
	}
	out := []Meaning{}
	sonarrs := Meaning{Order: "Sonarr", Season: season, Episode: episode, Missing: true}
	for _, e := range episodes {
		if e.SeasonNumber == season && e.EpisodeNumber == episode {
			sonarrs.Title, sonarrs.AirDate, sonarrs.Missing = e.Title, e.AirDate, false
			if t, ok := tmdbOf[e.ID]; ok {
				sonarrs.Translated, sonarrs.Runtime = t.Translated, t.Runtime
			}
		}
	}
	out = append(out, sonarrs)
	if orders == nil {
		return out
	}
	in := func(name string, seasons []overfin.OrderSeason) Meaning {
		m := Meaning{Order: name, Season: season, Episode: episode, Missing: true}
		for _, s := range seasons {
			if s.Season != season {
				continue
			}
			for _, e := range s.Episodes {
				if e.Episode == episode {
					m.Title, m.Translated, m.AirDate, m.Runtime, m.Missing = e.Name, e.Translated, e.AirDate, e.Runtime, false
				}
			}
		}
		return m
	}
	out = append(out, in("TMDB", orders.Seasons))
	for _, other := range orders.Others {
		// Only the orders that rearrange this season say something else.
		if slices.ContainsFunc(other.Seasons, func(s overfin.OrderSeason) bool { return s.Season == season }) {
			out = append(out, in(cmp.Or(other.Name, "Another order on TMDB"), other.Seasons))
		}
	}
	return out
}
