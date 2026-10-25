package sonarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// What adding series needs from Sonarr: its import lists, a series'
// details before it's added, adding it, searching for it, and whether that
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

// ImportList is one of Sonarr's import lists, with what a series it brings
// is added with.
type ImportList struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Implementation     string `json:"implementation"`
	EnableAutomaticAdd bool   `json:"enableAutomaticAdd"`
	RootFolderPath     string `json:"rootFolderPath"`
	QualityProfileID   int    `json:"qualityProfileId"`
	ShouldMonitor      string `json:"shouldMonitor"`
	SeriesType         string `json:"seriesType"`
	SeasonFolder       bool   `json:"seasonFolder"`
	Fields             []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	} `json:"fields"`
}

// CustomURL is the address of a custom list (Sonarr's "Custom List"), or
// "" for another kind of list.
func (list ImportList) CustomURL() string {
	if list.Implementation != "CustomImport" {
		return ""
	}
	for _, field := range list.Fields {
		if field.Name == "baseUrl" {
			address, _ := field.Value.(string)
			return strings.TrimSpace(address)
		}
	}
	return ""
}

func (client *Client) ImportLists() ([]ImportList, error) {
	var lists []ImportList
	return lists, client.send(http.MethodGet, "/api/v3/importlist", nil, &lists)
}

// ExcludedTVDBIDs returns the series Sonarr's import lists may not add.
func (client *Client) ExcludedTVDBIDs() (map[int]bool, error) {
	var exclusions []struct {
		TVDBID int `json:"tvdbId"`
	}
	if err := client.send(http.MethodGet, "/api/v3/importlistexclusion", nil, &exclusions); err != nil {
		return nil, err
	}
	excluded := map[int]bool{}
	for _, exclusion := range exclusions {
		excluded[exclusion.TVDBID] = true
	}
	return excluded, nil
}

// ListSeries is a series a custom list holds.
type ListSeries struct {
	// TVDBID: the custom list format says tvdbId; MDBList's tvdbid matches
	// too, since JSON names are matched regardless of case.
	TVDBID      int    `json:"tvdbId"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
	ReleaseYear int    `json:"release_year"`
}

// FetchCustomList reads a custom list from its address. Sonarr's key isn't
// sent: the list is somewhere else. A list that answers with a web page
// (MDBList does, to anything but Sonarr) is asked again at its address
// plus /json, where MDBList gives the JSON to anyone.
func FetchCustomList(ctx context.Context, address string) ([]ListSeries, error) {
	series, isPage, err := fetchList(ctx, address)
	if isPage {
		series, _, err = fetchList(ctx, strings.TrimRight(address, "/")+"/json")
	}
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", address, err)
	}
	for i := range series {
		if series[i].Year == 0 {
			series[i].Year = series[i].ReleaseYear
		}
	}
	return series, nil
}

// fetchList reads one address as a custom list; isPage says it answered
// with a web page instead.
func fetchList(ctx context.Context, address string) (series []ListSeries, isPage bool, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, false, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		return nil, false, fmt.Errorf("%s", response.Status)
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/html") {
		return nil, true, nil
	}
	return series, false, json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(&series)
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

// AddSeries adds the series, every season monitored, in season folders,
// tagged, to the root folder with the quality profile, without searching:
// the search is asked for apart, to follow it. It returns the series' ID
// in Sonarr.
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
	body["addOptions"] = map[string]any{"monitor": "all", "searchForMissingEpisodes": false, "searchForCutoffUnmetEpisodes": false}
	var added struct {
		ID int `json:"id"`
	}
	return added.ID, client.send(http.MethodPost, "/api/v3/series", body, &added)
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

// SearchSeries asks Sonarr to search for a series' monitored episodes, and
// returns the command's ID to follow it.
func (client *Client) SearchSeries(seriesID int) (int, error) {
	var command struct {
		ID int `json:"id"`
	}
	body := map[string]any{"name": "SeriesSearch", "seriesId": seriesID}
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

// SeriesHasFiles reports whether the series has at least one episode file.
// A series no longer in Sonarr gives an error IsNotFound recognizes.
func (client *Client) SeriesHasFiles(seriesID int) (bool, error) {
	var series struct {
		Statistics struct {
			EpisodeFileCount int `json:"episodeFileCount"`
		} `json:"statistics"`
	}
	return series.Statistics.EpisodeFileCount > 0, client.send(http.MethodGet, fmt.Sprintf("/api/v3/series/%d", seriesID), nil, &series)
}

// DeleteSeries takes a series out of Sonarr, leaving any file in place and
// adding no exclusion: it's for a series with no file, to be tried again.
func (client *Client) DeleteSeries(seriesID int) error {
	return client.send(http.MethodDelete, fmt.Sprintf("/api/v3/series/%d?deleteFiles=false&addImportListExclusion=false", seriesID), nil, nil)
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
