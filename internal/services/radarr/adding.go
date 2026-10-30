package radarr

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

// What adding films needs from Radarr: a film's details before it's
// added, where it goes, adding it, searching for it, and whether that
// search started a download.

// StatusError is Radarr answering with an error status.
type StatusError struct {
	Code   int
	Detail string
}

func (e *StatusError) Error() string { return fmt.Sprintf("radarr: %d %s", e.Code, e.Detail) }

// IsNotFound reports whether err is Radarr saying the thing doesn't exist.
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
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/movie/lookup/tmdb?tmdbId=%d", tmdbID), nil, &raw); err != nil {
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
	return added.ID, client.send(http.MethodPost, "/api/v3/movie", body, &added)
}

// TagAll adds the tag to every film in Radarr, and returns how many there
// are.
func (client *Client) TagAll(tagID int) (int, error) {
	var movies []struct {
		ID int `json:"id"`
	}
	if err := client.send(http.MethodGet, "/api/v3/movie", nil, &movies); err != nil {
		return 0, err
	}
	if len(movies) == 0 {
		return 0, nil
	}
	ids := make([]int, len(movies))
	for i, m := range movies {
		ids[i] = m.ID
	}
	return len(ids), client.send(http.MethodPut, "/api/v3/movie/editor", map[string]any{"movieIds": ids, "tags": []int{tagID}, "applyTags": "add"}, nil)
}

// TagMovie adds a tag to a film already in Radarr.
func (client *Client) TagMovie(movieID, tagID int) error {
	var movie map[string]any
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/movie/%d", movieID), nil, &movie); err != nil {
		return err
	}
	tags, _ := movie["tags"].([]any)
	for _, existing := range tags {
		if number, ok := existing.(float64); ok && int(number) == tagID {
			return nil
		}
	}
	movie["tags"] = append(tags, tagID)
	return client.send(http.MethodPut, fmt.Sprintf("/api/v3/movie/%d", movieID), movie, nil)
}

// SearchMovie asks Radarr to search for one film, and returns the
// command's ID to follow it.
func (client *Client) SearchMovie(movieID int) (int, error) {
	var command struct {
		ID int `json:"id"`
	}
	body := map[string]any{"name": "MoviesSearch", "movieIds": []int{movieID}}
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

// Queued reports whether the film has a download in Radarr's queue: from
// the grab until it's imported, finished ones waiting for import included.
func (client *Client) Queued(movieID int) (bool, error) {
	var queued []json.RawMessage
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/queue/details?movieId=%d", movieID), nil, &queued); err != nil {
		return false, err
	}
	return len(queued) > 0, nil
}

// GrabbedSince reports whether a download was started for the film since
// the given time: in Radarr's queue now, or grabbed in its history.
func (client *Client) GrabbedSince(movieID int, since time.Time) (bool, error) {
	var queued []json.RawMessage
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/queue/details?movieId=%d", movieID), nil, &queued); err != nil {
		return false, err
	}
	if len(queued) > 0 {
		return true, nil
	}
	var history []struct {
		EventType string    `json:"eventType"`
		Date      time.Time `json:"date"`
	}
	if err := client.send(http.MethodGet, fmt.Sprintf("/api/v3/history/movie?movieId=%d", movieID), nil, &history); err != nil {
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
// Radarr gives an error IsNotFound recognizes.
func (client *Client) MovieHasFile(movieID int) (bool, error) {
	var movie struct {
		HasFile bool `json:"hasFile"`
	}
	return movie.HasFile, client.send(http.MethodGet, fmt.Sprintf("/api/v3/movie/%d", movieID), nil, &movie)
}

// Destination is where a title Overtide adds goes: Radarr's root folder
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
		return "", 0, fmt.Errorf("found %d root folders and %d quality profiles in Radarr; Overtide adds only with one of each", len(folders), len(profiles))
	}
	return folders[0].Path, profiles[0].ID, nil
}
