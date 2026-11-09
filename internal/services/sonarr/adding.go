package sonarr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/overfin/overtide/internal/services/arr"
)

// What adding series needs from Sonarr: a series' details before it's
// added, where it goes, adding it, searching for it, and whether that
// search started a download. Where it goes is arr's Destination.

// How long WaitAdded waits, and how often it looks: the refresh takes
// seconds, more when Sonarr is busy; it's listed by the time the series is
// added, so the grace is only for a Sonarr that doesn't say.
const (
	addedRefreshLimit = 45 * time.Second
	addedRefreshGrace = 2 * time.Second
	addedRefreshPoll  = 200 * time.Millisecond
)

// updateSeries reads the series whole, lets change edit it, and saves it
// unless change says there's nothing to save.
func (client *Client) updateSeries(id int, change func(series map[string]any) bool) error {
	var series map[string]any
	if err := client.Get(fmt.Sprintf("/api/v3/series/%d", id), &series); err != nil {
		return err
	}
	if !change(series) {
		return nil
	}
	return client.Send(http.MethodPut, fmt.Sprintf("/api/v3/series/%d", id), series, nil)
}

// SeriesLookup is a series as Sonarr knows it before it's added.
type SeriesLookup struct {
	Title      string     `json:"title"`
	Year       int        `json:"year"`
	Status     string     `json:"status"`
	FirstAired *time.Time `json:"firstAired"`
	// raw is the whole answer: adding the series sends it back.
	raw map[string]any
}

func (client *Client) LookupSeries(tvdbID int) (SeriesLookup, error) {
	var raws []map[string]any
	if err := client.Get(fmt.Sprintf("/api/v3/series/lookup?term=tvdb:%d", tvdbID), &raws); err != nil {
		return SeriesLookup{}, err
	}
	if len(raws) == 0 {
		return SeriesLookup{}, &arr.StatusError{App: "Sonarr", Code: http.StatusNotFound, Detail: fmt.Sprintf("no series with TVDB ID %d", tvdbID)}
	}
	encoded, err := json.Marshal(raws[0])
	if err != nil {
		return SeriesLookup{}, err
	}
	var lookup SeriesLookup
	if err := json.Unmarshal(encoded, &lookup); err != nil {
		return SeriesLookup{}, err
	}
	lookup.raw = raws[0]
	return lookup, nil
}

// AddSeries adds the series, tagged, in season folders, to the root
// folder with the quality profile, with no season monitored: Sonarr grabs
// nothing for it until seasons are (SetSeasonsMonitored). It returns the
// series' ID in Sonarr.
func (client *Client) AddSeries(lookup SeriesLookup, rootFolder string, qualityProfileID int, tagID int) (int, error) {
	body := map[string]any{}
	for key, value := range lookup.raw {
		body[key] = value
	}
	body["rootFolderPath"] = rootFolder
	body["qualityProfileId"] = qualityProfileID
	body["monitored"] = true
	body["seasonFolder"] = true
	body["seriesType"] = "standard"
	body["tags"] = []int{tagID}
	body["addOptions"] = map[string]any{"monitor": "none", "searchForMissingEpisodes": false, "searchForCutoffUnmetEpisodes": false}
	var added struct {
		ID int `json:"id"`
	}
	return added.ID, client.Send(http.MethodPost, "/api/v3/series", body, &added)
}

// WaitAdded waits for the refresh Sonarr starts on a series just added, at
// most a little while: at its end, it applies the added "monitor: none" to
// the seasons, undoing any monitoring done before, and a season search then
// searches nothing. Unknown or too long, whatever comes next is tried anyway.
func (client *Client) WaitAdded(seriesID int) {
	deadline := time.Now().Add(addedRefreshLimit)
	notSeen := time.Now().Add(addedRefreshGrace)
	seen := false
	for time.Now().Before(deadline) {
		var commands []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Body   struct {
				SeriesIDs []int `json:"seriesIds"`
			} `json:"body"`
		}
		if err := client.Get("/api/v3/command", &commands); err != nil {
			return
		}
		running, listed := false, false
		for _, command := range commands {
			if command.Name != "RefreshSeries" || !slices.Contains(command.Body.SeriesIDs, seriesID) {
				continue
			}
			listed = true
			running = running || command.Status == "queued" || command.Status == "started"
		}
		seen = seen || listed
		if (seen && !running) || (!seen && time.Now().After(notSeen)) {
			return
		}
		time.Sleep(addedRefreshPoll)
	}
}

// SetSeasonsMonitored monitors some of a series' seasons, or stops (Sonarr
// carries a season's change to its episodes); the series itself stays
// monitored, so its monitored seasons get new episodes and upgrades.
// Unmonitored, Sonarr grabs nothing for a season, but still imports a
// download already started.
func (client *Client) SetSeasonsMonitored(seriesID int, seasons []int, monitored bool) error {
	wanted := map[int]bool{}
	for _, n := range seasons {
		wanted[n] = true
	}
	return client.updateSeries(seriesID, func(series map[string]any) bool {
		list, _ := series["seasons"].([]any)
		for _, raw := range list {
			season, _ := raw.(map[string]any)
			if number, ok := season["seasonNumber"].(float64); ok && wanted[int(number)] {
				season["monitored"] = monitored
			}
		}
		series["monitored"] = true
		return true
	})
}

// SeasonsHaveFiles reports whether each of the seasons has at least one
// episode file. A series no longer in Sonarr gives an error arr.IsNotFound
// recognizes.
func (client *Client) SeasonsHaveFiles(seriesID int, seasons []int) (bool, error) {
	var series struct {
		Seasons []struct {
			SeasonNumber int `json:"seasonNumber"`
			Statistics   struct {
				EpisodeFileCount int `json:"episodeFileCount"`
			} `json:"statistics"`
		} `json:"seasons"`
	}
	if err := client.Get(fmt.Sprintf("/api/v3/series/%d", seriesID), &series); err != nil {
		return false, err
	}
	files := map[int]int{}
	for _, season := range series.Seasons {
		files[season.SeasonNumber] = season.Statistics.EpisodeFileCount
	}
	for _, n := range seasons {
		if files[n] == 0 {
			return false, nil
		}
	}
	return len(seasons) > 0, nil
}

// TagSeries adds a tag to a series already in Sonarr.
func (client *Client) TagSeries(seriesID, tagID int) error {
	return client.updateSeries(seriesID, func(series map[string]any) bool {
		tags, _ := series["tags"].([]any)
		for _, existing := range tags {
			if number, ok := existing.(float64); ok && int(number) == tagID {
				return false
			}
		}
		series["tags"] = append(tags, tagID)
		return true
	})
}

// SearchSeason asks Sonarr to search for a season's monitored episodes,
// and returns the command's ID to follow it.
func (client *Client) SearchSeason(seriesID, season int) (int, error) {
	return client.Command(map[string]any{"name": "SeasonSearch", "seriesId": seriesID, "seasonNumber": season})
}

// Queued reports whether the series has a download in Sonarr's queue:
// from the grab until it's imported, finished ones waiting for import
// included.
func (client *Client) Queued(seriesID int) (bool, error) {
	items, err := client.Queue(seriesID)
	return len(items) > 0, err
}

// GrabbedSince reports whether a download was started for any episode of
// the series since the given time: in Sonarr's queue now, or grabbed in
// its history.
func (client *Client) GrabbedSince(seriesID int, since time.Time) (bool, error) {
	if queued, err := client.Queued(seriesID); err != nil || queued {
		return queued, err
	}
	var history []struct {
		EventType string    `json:"eventType"`
		Date      time.Time `json:"date"`
	}
	if err := client.Get(fmt.Sprintf("/api/v3/history/series?seriesId=%d", seriesID), &history); err != nil {
		return false, err
	}
	for _, event := range history {
		if event.EventType == "grabbed" && !event.Date.Before(since) {
			return true, nil
		}
	}
	return false, nil
}
