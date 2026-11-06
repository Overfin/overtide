package radarr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// What adding films needs from Radarr: a film's details before it's
// added, adding it, searching for it, and whether that search started a
// download.

// MovieLookup is a film as Radarr knows it before it's added.
type MovieLookup struct {
	Title           string     `json:"title"`
	Year            int        `json:"year"`
	Status          string     `json:"status"`
	InCinemas       *time.Time `json:"inCinemas"`
	DigitalRelease  *time.Time `json:"digitalRelease"`
	PhysicalRelease *time.Time `json:"physicalRelease"`
	// raw is the whole answer: adding the film sends it back.
	raw map[string]any
}

func (client *Client) LookupMovie(tmdbID int) (MovieLookup, error) {
	var raw map[string]any
	if err := client.Get(fmt.Sprintf("/api/v3/movie/lookup/tmdb?tmdbId=%d", tmdbID), &raw); err != nil {
		return MovieLookup{}, err
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return MovieLookup{}, err
	}
	var lookup MovieLookup
	if err := json.Unmarshal(encoded, &lookup); err != nil {
		return MovieLookup{}, err
	}
	lookup.raw = raw
	return lookup, nil
}

// AddMovie adds the film, unmonitored and tagged, to the root folder with
// the quality profile: Radarr grabs nothing for it until it's monitored
// (SetMonitored; unmonitored, Radarr still imports a download already
// started). It returns the film's ID in Radarr.
func (client *Client) AddMovie(lookup MovieLookup, rootFolder string, qualityProfileID int, tagID int) (int, error) {
	body := map[string]any{}
	for key, value := range lookup.raw {
		body[key] = value
	}
	body["rootFolderPath"] = rootFolder
	body["qualityProfileId"] = qualityProfileID
	body["monitored"] = false
	body["minimumAvailability"] = "released"
	body["tags"] = []int{tagID}
	body["addOptions"] = map[string]any{"searchForMovie": false, "monitor": "none"}
	var added struct {
		ID int `json:"id"`
	}
	return added.ID, client.Send(http.MethodPost, "/api/v3/movie", body, &added)
}

// TagMovie adds a tag to a film already in Radarr.
func (client *Client) TagMovie(movieID, tagID int) error {
	return client.updateMovie(movieID, func(movie map[string]any) {
		tags, _ := movie["tags"].([]any)
		for _, existing := range tags {
			if number, ok := existing.(float64); ok && int(number) == tagID {
				return
			}
		}
		movie["tags"] = append(tags, tagID)
	})
}

// SearchMovie asks Radarr to search for one film, and returns the
// command's ID to follow it.
func (client *Client) SearchMovie(movieID int) (int, error) {
	return client.Command(map[string]any{"name": "MoviesSearch", "movieIds": []int{movieID}})
}

// QueueItem is a download in Radarr's queue.
type QueueItem struct {
	MovieID int    `json:"movieId"`
	Title   string `json:"title"`
	// Status is the download's (queued, downloading, completed, failed…);
	// TrackedDownloadState is Radarr's import of it.
	Status               string `json:"status"`
	TrackedDownloadState string `json:"trackedDownloadState"`
	// Size and SizeLeft are in bytes; EstimatedCompletion is when the
	// download client thinks it's done, nil when it doesn't say.
	Size                float64    `json:"size"`
	SizeLeft            float64    `json:"sizeleft"`
	EstimatedCompletion *time.Time `json:"estimatedCompletionTime"`
}

// FullQueue returns every download in Radarr's queue.
func (client *Client) FullQueue() ([]QueueItem, error) {
	var items []QueueItem
	return items, client.Get("/api/v3/queue/details", &items)
}

// Queued reports whether the film has a download in Radarr's queue: from
// the grab until it's imported, finished ones waiting for import included.
func (client *Client) Queued(movieID int) (bool, error) {
	var queued []json.RawMessage
	if err := client.Get(fmt.Sprintf("/api/v3/queue/details?movieId=%d", movieID), &queued); err != nil {
		return false, err
	}
	return len(queued) > 0, nil
}

// GrabbedSince reports whether a download was started for the film since
// the given time: in Radarr's queue now, or grabbed in its history.
func (client *Client) GrabbedSince(movieID int, since time.Time) (bool, error) {
	if queued, err := client.Queued(movieID); err != nil || queued {
		return queued, err
	}
	var history []struct {
		EventType string    `json:"eventType"`
		Date      time.Time `json:"date"`
	}
	if err := client.Get(fmt.Sprintf("/api/v3/history/movie?movieId=%d", movieID), &history); err != nil {
		return false, err
	}
	for _, event := range history {
		if event.EventType == "grabbed" && !event.Date.Before(since) {
			return true, nil
		}
	}
	return false, nil
}

// MovieHasFile reports whether the film has its file. A film no longer in
// Radarr gives an error arr.IsNotFound recognizes.
func (client *Client) MovieHasFile(movieID int) (bool, error) {
	var movie struct {
		HasFile bool `json:"hasFile"`
	}
	return movie.HasFile, client.Get(fmt.Sprintf("/api/v3/movie/%d", movieID), &movie)
}
