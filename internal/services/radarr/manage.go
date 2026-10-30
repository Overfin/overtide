package radarr

import (
	"net/http"
	"time"
)

// Title is a film as managing it needs: its tags, whether Radarr looks for
// it, whether it has its file, and when it was added.
type Title struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Year      int       `json:"year"`
	TMDBID    int       `json:"tmdbId"`
	Tags      []int     `json:"tags"`
	Monitored bool      `json:"monitored"`
	HasFile   bool      `json:"hasFile"`
	Added     time.Time `json:"added"`
}

// Titles returns every film in Radarr.
func (client *Client) Titles() ([]Title, error) {
	var titles []Title
	return titles, client.send(http.MethodGet, "/api/v3/movie", nil, &titles)
}

// TagLabels returns Radarr's tags, by ID.
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

// EditTags adds the tag to the films, or takes it off them, in one call.
func (client *Client) EditTags(ids []int, tagID int, add bool) error {
	if len(ids) == 0 {
		return nil
	}
	apply := "remove"
	if add {
		apply = "add"
	}
	return client.send(http.MethodPut, "/api/v3/movie/editor", map[string]any{"movieIds": ids, "tags": []int{tagID}, "applyTags": apply}, nil)
}

// SetManyMonitored monitors the films, or stops, in one call.
func (client *Client) SetManyMonitored(ids []int, monitored bool) error {
	if len(ids) == 0 {
		return nil
	}
	return client.send(http.MethodPut, "/api/v3/movie/editor", map[string]any{"movieIds": ids, "monitored": monitored}, nil)
}
