// Package radarr is Overtide's client of Radarr: the films and their files
// for the inventory and removals, and what adding films needs. What Radarr
// and Sonarr share is in arr.
package radarr

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/arr"
)

type Client struct{ *arr.Client }

func New(base, key string) *Client { return &Client{arr.New("Radarr", base, key)} }

func (client *Client) WithContext(ctx context.Context) *Client {
	return &Client{client.Client.WithContext(ctx)}
}

// Title is a film: its tags, whether Radarr looks for it, whether it has
// its file, and when it was added.
type Title struct {
	ID         int       `json:"id"`
	Title      string    `json:"title"`
	Year       int       `json:"year"`
	Path       string    `json:"path"`
	SizeOnDisk int64     `json:"sizeOnDisk"`
	TMDBID     int       `json:"tmdbId"`
	IMDBID     string    `json:"imdbId"`
	Tags       []int     `json:"tags"`
	Monitored  bool      `json:"monitored"`
	HasFile    bool      `json:"hasFile"`
	Added      time.Time `json:"added"`
}

// Titles returns every film in Radarr.
func (client *Client) Titles() ([]Title, error) {
	var titles []Title
	return titles, client.Get("/api/v3/movie", &titles)
}

func (client *Client) Inventory() ([]model.Media, error) {
	var titles []Title
	var labels map[int]string
	var mediaErr, tagErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); titles, mediaErr = client.Titles() }()
	go func() { defer wg.Done(); labels, tagErr = client.TagLabels() }()
	wg.Wait()
	if mediaErr != nil {
		return nil, mediaErr
	}
	if tagErr != nil {
		return nil, fmt.Errorf("radarr tags: %w", tagErr)
	}
	out := make([]model.Media, 0, len(titles))
	for _, m := range titles {
		out = append(out, model.Media{Type: model.Movie, SourceID: m.ID, Title: m.Title, Year: m.Year, Path: m.Path, SizeBytes: m.SizeOnDisk, AddedAt: m.Added, Tags: arr.Labels(m.Tags, labels), TMDBID: m.TMDBID, IMDBID: m.IMDBID})
	}
	return out, nil
}

type FileRecord struct {
	ID        int       `json:"id"`
	MovieID   int       `json:"movieId"`
	Relative  string    `json:"relativePath"`
	Size      int64     `json:"size"`
	DateAdded time.Time `json:"dateAdded"`
}

// Files returns authoritative current movie files in bounded bulk requests.
// Radarr accepts repeated movieId query parameters, so this avoids one request
// per movie while keeping URLs to a reasonable size.
func (client *Client) Files(movieIDs []int) ([]FileRecord, error) {
	if len(movieIDs) == 0 {
		return nil, nil
	}
	const chunk = 100
	out := make([]FileRecord, 0)
	for start := 0; start < len(movieIDs); start += chunk {
		end := min(start+chunk, len(movieIDs))
		q := url.Values{}
		for _, id := range movieIDs[start:end] {
			q.Add("movieId", fmt.Sprint(id))
		}
		var xs []FileRecord
		if err := client.Get("/api/v3/moviefile?"+q.Encode(), &xs); err != nil {
			return nil, err
		}
		out = append(out, xs...)
	}
	return out, nil
}

func (client *Client) DeleteFile(id int) error {
	return client.Send(http.MethodDelete, fmt.Sprintf("/api/v3/moviefile/%d", id), nil, nil)
}

// updateMovie reads the film whole, lets change edit it, and saves it.
func (client *Client) updateMovie(id int, change func(movie map[string]any)) error {
	var movie map[string]any
	if err := client.Get(fmt.Sprintf("/api/v3/movie/%d", id), &movie); err != nil {
		return err
	}
	change(movie)
	return client.Send(http.MethodPut, fmt.Sprintf("/api/v3/movie/%d", id), movie, nil)
}

func (client *Client) SetMonitored(id int, monitored bool) error {
	return client.updateMovie(id, func(movie map[string]any) { movie["monitored"] = monitored })
}

// AddImportListExclusion prevents a configured Radarr import list from
// re-adding a movie whose managed files were intentionally removed.
func (client *Client) AddImportListExclusion(title string, year, tmdbID int) error {
	if tmdbID <= 0 {
		return fmt.Errorf("radarr import-list exclusion requires a TMDB id")
	}
	body := struct {
		MovieTitle string `json:"movieTitle"`
		TMDBID     int    `json:"tmdbId"`
		MovieYear  int    `json:"movieYear"`
	}{MovieTitle: title, TMDBID: tmdbID, MovieYear: year}
	return client.Send(http.MethodPost, "/api/v3/exclusions", body, nil)
}
