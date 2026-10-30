package sonarr

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// Title is a series as managing it needs: its tags, whether Sonarr looks
// for it, and when it was added.
type Title struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	TVDBID    int       `json:"tvdbId"`
	Tags      []int     `json:"tags"`
	Monitored bool      `json:"monitored"`
	Added     time.Time `json:"added"`
	// MonitorNewItems is "all" when Sonarr monitors new seasons itself.
	MonitorNewItems string `json:"monitorNewItems"`
	Seasons         []struct {
		SeasonNumber int  `json:"seasonNumber"`
		Monitored    bool `json:"monitored"`
	} `json:"seasons"`
}

// Taken reports whether Sonarr has nothing to grab for the series on its
// own: no season monitored, nor new ones to be (see TakeSeries).
func (title Title) Taken() bool {
	if title.MonitorNewItems != "none" {
		return false
	}
	for _, season := range title.Seasons {
		if season.Monitored {
			return false
		}
	}
	return true
}

// Titles returns every series in Sonarr.
func (client *Client) Titles() ([]Title, error) {
	var titles []Title
	return titles, client.send(http.MethodGet, "/api/v3/series", nil, &titles)
}

// TagLabels returns Sonarr's tags, by ID.
func (client *Client) TagLabels() (map[int]string, error) {
	var tags []tag
	if err := client.send(http.MethodGet, "/api/v3/tag", nil, &tags); err != nil {
		return nil, err
	}
	labels := make(map[int]string, len(tags))
	for _, t := range tags {
		labels[t.ID] = t.Label
	}
	return labels, nil
}

// EditTags adds the tag to the series, or takes it off them, in one call.
func (client *Client) EditTags(ids []int, tagID int, add bool) error {
	if len(ids) == 0 {
		return nil
	}
	apply := "remove"
	if add {
		apply = "add"
	}
	return client.send(http.MethodPut, "/api/v3/series/editor", map[string]any{"seriesIds": ids, "tags": []int{tagID}, "applyTags": apply}, nil)
}

// Episode is one episode: whether it has its file, whether Sonarr looks
// for it, and when it airs.
type Episode struct {
	ID            int       `json:"id"`
	SeriesID      int       `json:"seriesId"`
	SeasonNumber  int       `json:"seasonNumber"`
	EpisodeNumber int       `json:"episodeNumber"`
	HasFile       bool      `json:"hasFile"`
	EpisodeFileID int       `json:"episodeFileId"`
	Monitored     bool      `json:"monitored"`
	AirDateUTC    time.Time `json:"airDateUtc"`
}

// Episodes returns a series' episodes.
func (client *Client) Episodes(seriesID int) ([]Episode, error) {
	var episodes []Episode
	return episodes, client.send(http.MethodGet, fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID), nil, &episodes)
}

// Calendar returns the episodes airing between start and end, monitored
// or not.
func (client *Client) Calendar(start, end time.Time) ([]Episode, error) {
	query := url.Values{"start": {start.UTC().Format(time.RFC3339)}, "end": {end.UTC().Format(time.RFC3339)}, "unmonitored": {"true"}}
	var episodes []Episode
	return episodes, client.send(http.MethodGet, "/api/v3/calendar?"+query.Encode(), nil, &episodes)
}

// SearchEpisodes asks Sonarr to search for these episodes, and returns the
// command's ID to follow it. Sonarr's episode search doesn't look at
// monitoring: the episodes needn't be monitored.
func (client *Client) SearchEpisodes(ids []int) (int, error) {
	var command struct {
		ID int `json:"id"`
	}
	return command.ID, client.send(http.MethodPost, "/api/v3/command", map[string]any{"name": "EpisodeSearch", "episodeIds": ids}, &command)
}

// TakeSeries leaves Sonarr nothing to grab on its own for the series:
// every season unmonitored (Sonarr carries it to their episodes), and new
// seasons not monitored when they appear. The series stays monitored, so
// the episodes Overtide monitors are grabbed.
func (client *Client) TakeSeries(seriesID int) error {
	var series map[string]any
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/series/%d", seriesID), nil, &series); err != nil {
		return err
	}
	seasons, _ := series["seasons"].([]any)
	for _, raw := range seasons {
		if season, ok := raw.(map[string]any); ok {
			season["monitored"] = false
		}
	}
	series["monitored"] = true
	series["monitorNewItems"] = "none"
	return client.send(http.MethodPut, fmt.Sprintf("/api/v3/series/%d", seriesID), series, nil)
}

// MissingEpisodes returns a page of the episodes that aired without their
// file, the monitored ones or the others, the most recent first, and how
// many there are.
func (client *Client) MissingEpisodes(monitored bool, page, pageSize int) ([]Episode, int, error) {
	query := url.Values{"page": {fmt.Sprint(page)}, "pageSize": {fmt.Sprint(pageSize)}, "monitored": {fmt.Sprint(monitored)},
		"sortKey": {"episodes.airDateUtc"}, "sortDirection": {"descending"}}
	var missing struct {
		TotalRecords int       `json:"totalRecords"`
		Records      []Episode `json:"records"`
	}
	if err := client.send(http.MethodGet, "/api/v3/wanted/missing?"+query.Encode(), nil, &missing); err != nil {
		return nil, 0, err
	}
	return missing.Records, missing.TotalRecords, nil
}

// GetEpisode returns one episode. One no longer in Sonarr gives an error
// IsNotFound recognizes.
func (client *Client) GetEpisode(id int) (Episode, error) {
	var episode Episode
	return episode, client.send(http.MethodGet, fmt.Sprintf("/api/v3/episode/%d", id), nil, &episode)
}

// QueuedEpisodes returns the IDs of the episodes with a download in
// Sonarr's queue.
func (client *Client) QueuedEpisodes() (map[int]bool, error) {
	var queued []struct {
		EpisodeID int `json:"episodeId"`
	}
	if err := client.send(http.MethodGet, "/api/v3/queue/details", nil, &queued); err != nil {
		return nil, err
	}
	ids := make(map[int]bool, len(queued))
	for _, q := range queued {
		ids[q.EpisodeID] = true
	}
	return ids, nil
}

// prowlarrIndexer matches an indexer Prowlarr made: its address is
// Prowlarr's, then the tracker's ID in Prowlarr.
var prowlarrIndexer = regexp.MustCompile(`^(https?://.+?)/(\d+)/?$`)

// ProwlarrURL returns Prowlarr's address, read from the indexers Prowlarr
// made here ("" without any).
func (client *Client) ProwlarrURL() (string, error) {
	var indexers []struct {
		Fields []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"fields"`
	}
	if err := client.send(http.MethodGet, "/api/v3/indexer", nil, &indexers); err != nil {
		return "", err
	}
	for _, indexer := range indexers {
		for _, field := range indexer.Fields {
			if base, ok := field.Value.(string); ok && field.Name == "baseUrl" {
				if match := prowlarrIndexer.FindStringSubmatch(base); match != nil {
					return match[1], nil
				}
			}
		}
	}
	return "", nil
}
