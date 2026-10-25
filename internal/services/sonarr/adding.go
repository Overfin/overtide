package sonarr

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// What adding series needs from Sonarr: a series' details before it's
// added, where it goes, adding it, searching for it, and whether that
// search started a download.

// StatusError is Sonarr answering with an error status.
type StatusError struct {
	Code   int
	Detail string
}

func (e *StatusError) Error() string { return fmt.Sprintf("sonarr: %d %s", e.Code, e.Detail) }

// IsNotFound reports whether err is Sonarr saying the thing doesn't exist.
func IsNotFound(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && status.Code == http.StatusNotFound
}

// send makes one request, with body as JSON when it isn't nil, and decodes
// the answer into out when out isn't nil.
func (client *Client) send(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(client.requestContext(), method, client.base+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", client.key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.hc.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return &StatusError{Code: response.StatusCode, Detail: strings.TrimSpace(response.Status + " " + string(detail))}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
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
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/series/lookup?term=tvdb:%d", tvdbID), nil, &raws); err != nil {
		return SeriesLookup{}, err
	}
	if len(raws) == 0 {
		return SeriesLookup{}, &StatusError{Code: http.StatusNotFound, Detail: fmt.Sprintf("no series with TVDB ID %d", tvdbID)}
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

// EnsureTag returns the ID of the tag with this label, made if needed.
func (client *Client) EnsureTag(label string) (int, error) {
	var tags []tag
	if err := client.send(http.MethodGet, "/api/v3/tag", nil, &tags); err != nil {
		return 0, err
	}
	for _, t := range tags {
		if strings.EqualFold(t.Label, label) {
			return t.ID, nil
		}
	}
	var made tag
	return made.ID, client.send(http.MethodPost, "/api/v3/tag", map[string]string{"label": label}, &made)
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
	return added.ID, client.send(http.MethodPost, "/api/v3/series", body, &added)
}

// SetSeasonsMonitored monitors some of a series' seasons, or stops (Sonarr
// carries a season's change to its episodes); the series itself stays
// monitored, so its monitored seasons get new episodes and upgrades.
// Unmonitored, Sonarr grabs nothing for a season, but still imports a
// download already started.
func (client *Client) SetSeasonsMonitored(seriesID int, seasons []int, monitored bool) error {
	var series map[string]any
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/series/%d", seriesID), nil, &series); err != nil {
		return err
	}
	wanted := map[int]bool{}
	for _, n := range seasons {
		wanted[n] = true
	}
	list, _ := series["seasons"].([]any)
	for _, raw := range list {
		season, _ := raw.(map[string]any)
		if number, ok := season["seasonNumber"].(float64); ok && wanted[int(number)] {
			season["monitored"] = monitored
		}
	}
	series["monitored"] = true
	return client.send(http.MethodPut, fmt.Sprintf("/api/v3/series/%d", seriesID), series, nil)
}

// SeasonsHaveFiles reports whether each of the seasons has at least one
// episode file. A series no longer in Sonarr gives an error IsNotFound
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
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/series/%d", seriesID), nil, &series); err != nil {
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

// TagAll adds the tag to every series in Sonarr, and returns how many
// there are.
func (client *Client) TagAll(tagID int) (int, error) {
	var all []struct {
		ID int `json:"id"`
	}
	if err := client.send(http.MethodGet, "/api/v3/series", nil, &all); err != nil {
		return 0, err
	}
	if len(all) == 0 {
		return 0, nil
	}
	ids := make([]int, len(all))
	for i, s := range all {
		ids[i] = s.ID
	}
	return len(ids), client.send(http.MethodPut, "/api/v3/series/editor", map[string]any{"seriesIds": ids, "tags": []int{tagID}, "applyTags": "add"}, nil)
}

// TagSeries adds a tag to a series already in Sonarr.
func (client *Client) TagSeries(seriesID, tagID int) error {
	var series map[string]any
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/series/%d", seriesID), nil, &series); err != nil {
		return err
	}
	tags, _ := series["tags"].([]any)
	for _, existing := range tags {
		if number, ok := existing.(float64); ok && int(number) == tagID {
			return nil
		}
	}
	series["tags"] = append(tags, tagID)
	return client.send(http.MethodPut, fmt.Sprintf("/api/v3/series/%d", seriesID), series, nil)
}

// SearchSeason asks Sonarr to search for a season's monitored episodes,
// and returns the command's ID to follow it.
func (client *Client) SearchSeason(seriesID, season int) (int, error) {
	var command struct {
		ID int `json:"id"`
	}
	body := map[string]any{"name": "SeasonSearch", "seriesId": seriesID, "seasonNumber": season}
	return command.ID, client.send(http.MethodPost, "/api/v3/command", body, &command)
}

// CommandStatus is queued, started, completed, failed, aborted or
// cancelled.
func (client *Client) CommandStatus(commandID int) (string, error) {
	var command struct {
		Status string `json:"status"`
	}
	return command.Status, client.send(http.MethodGet, fmt.Sprintf("/api/v3/command/%d", commandID), nil, &command)
}

// GrabbedSince reports whether a download was started for any episode of
// the series since the given time: in Sonarr's queue now, or grabbed in
// its history.
func (client *Client) GrabbedSince(seriesID int, since time.Time) (bool, error) {
	var queued []json.RawMessage
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/queue/details?seriesId=%d", seriesID), nil, &queued); err != nil {
		return false, err
	}
	if len(queued) > 0 {
		return true, nil
	}
	var history []struct {
		EventType string    `json:"eventType"`
		Date      time.Time `json:"date"`
	}
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/history/series?seriesId=%d", seriesID), nil, &history); err != nil {
		return false, err
	}
	for _, event := range history {
		if event.EventType == "grabbed" && !event.Date.Before(since) {
			return true, nil
		}
	}
	return false, nil
}

// Destination is where a title Overtide adds goes: Sonarr's root folder
// and quality profile. With several of either, which one isn't Overtide's
// guess, so it's an error.
func (client *Client) Destination() (string, int, error) {
	var folders []RootFolder
	if err := client.send(http.MethodGet, "/api/v3/rootfolder", nil, &folders); err != nil {
		return "", 0, err
	}
	var profiles []struct {
		ID int `json:"id"`
	}
	if err := client.send(http.MethodGet, "/api/v3/qualityprofile", nil, &profiles); err != nil {
		return "", 0, err
	}
	if len(folders) != 1 || len(profiles) != 1 {
		return "", 0, fmt.Errorf("found %d root folders and %d quality profiles in Sonarr; Overtide adds only with one of each", len(folders), len(profiles))
	}
	return folders[0].Path, profiles[0].ID, nil
}
