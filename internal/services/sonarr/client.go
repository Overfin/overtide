// Package sonarr is Overtide's client of Sonarr: the series and their
// files for the inventory and removals, and what adding series needs:
// seasons, episodes, releases, the queue and imports. What Radarr and
// Sonarr share is in arr.
package sonarr

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/arr"
)

type Client struct{ *arr.Client }

func New(base, key string) *Client { return &Client{arr.New("Sonarr", base, key)} }

func (client *Client) WithContext(ctx context.Context) *Client {
	return &Client{client.Client.WithContext(ctx)}
}

func (client *Client) Inventory() ([]model.Media, error) {
	var titles []Title
	var labels map[int]string
	var seriesErr, tagErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); titles, seriesErr = client.Titles() }()
	go func() { defer wg.Done(); labels, tagErr = client.TagLabels() }()
	wg.Wait()
	if seriesErr != nil {
		return nil, seriesErr
	}
	if tagErr != nil {
		return nil, fmt.Errorf("sonarr tags: %w", tagErr)
	}
	out := make([]model.Media, 0, len(titles))
	for _, s := range titles {
		out = append(out, model.Media{Type: model.Series, SourceID: s.ID, Title: s.Title, Year: s.Year, Path: s.Path, SizeBytes: s.Statistics.SizeOnDisk, AddedAt: s.Added, Tags: arr.Labels(s.Tags, labels), TMDBID: s.TMDBID, TVDBID: s.TVDBID, IMDBID: s.IMDBID})
	}
	return out, nil
}

// FileRecord is an episode file. Files fills SeasonNumber and Parts from
// the episodes on it.
type FileRecord struct {
	ID           int                   `json:"id"`
	SeriesID     int                   `json:"seriesId"`
	SeasonNumber int                   `json:"seasonNumber"`
	Relative     string                `json:"relativePath"`
	Size         int64                 `json:"size"`
	DateAdded    time.Time             `json:"dateAdded"`
	Parts        []model.MediaFilePart `json:"-"`
}

// EpisodeFiles returns a series' episode files, as Sonarr gives them.
func (client *Client) EpisodeFiles(seriesID int) ([]FileRecord, error) {
	var files []FileRecord
	return files, client.Get(fmt.Sprintf("/api/v3/episodefile?seriesId=%d", seriesID), &files)
}

// Files returns authoritative current episode files. Sonarr's v3 endpoint is
// scoped to one series, so reconciliation uses a small worker pool and is kept
// out of the normal inventory refresh path.
func (client *Client) Files(seriesIDs []int) ([]FileRecord, error) {
	if len(seriesIDs) == 0 {
		return nil, nil
	}
	type result struct {
		files []FileRecord
		err   error
	}
	jobs := make(chan int)
	results := make(chan result, len(seriesIDs))
	workers := min(8, len(seriesIDs))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				var xs []FileRecord
				var episodes []Episode
				var filesErr, episodesErr error
				var calls sync.WaitGroup
				calls.Add(2)
				go func() { defer calls.Done(); xs, filesErr = client.EpisodeFiles(id) }()
				go func() { defer calls.Done(); episodes, episodesErr = client.Episodes(id) }()
				calls.Wait()
				if filesErr != nil {
					results <- result{nil, filesErr}
					continue
				}
				if episodesErr != nil {
					results <- result{nil, episodesErr}
					continue
				}
				parts := map[int][]model.MediaFilePart{}
				seasonByFileID := map[int]int{}
				for _, e := range episodes {
					if e.EpisodeFileID <= 0 {
						continue
					}
					label := fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber)
					if strings.TrimSpace(e.Title) != "" {
						label += " · " + e.Title
					}
					parts[e.EpisodeFileID] = append(parts[e.EpisodeFileID], model.MediaFilePart{
						Group: fmt.Sprintf("Season %d", e.SeasonNumber), Label: label,
						Order: e.SeasonNumber*100000 + e.EpisodeNumber, SourcePartID: e.ID,
						AiredAt: e.AirDateUTC,
					})
					seasonByFileID[e.EpisodeFileID] = e.SeasonNumber
				}
				for i := range xs {
					xs[i].SeriesID = id
					xs[i].SeasonNumber = seasonByFileID[xs[i].ID]
					xs[i].Parts = append([]model.MediaFilePart(nil), parts[xs[i].ID]...)
				}
				results <- result{xs, nil}
			}
		}()
	}
	go func() {
		for _, id := range seriesIDs {
			jobs <- id
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	out := make([]FileRecord, 0)
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		out = append(out, r.files...)
	}
	return out, nil
}

func (client *Client) DeleteFile(id int) error {
	return client.Send(http.MethodDelete, fmt.Sprintf("/api/v3/episodefile/%d", id), nil, nil)
}

func (client *Client) SetEpisodesMonitored(ids []int, monitored bool) error {
	if len(ids) == 0 {
		return nil
	}
	body := struct {
		EpisodeIDs []int `json:"episodeIds"`
		Monitored  bool  `json:"monitored"`
	}{EpisodeIDs: ids, Monitored: monitored}
	return client.Send(http.MethodPut, "/api/v3/episode/monitor", body, nil)
}

// AddImportListExclusion prevents a configured Sonarr import list from
// re-adding a series whose managed files were intentionally removed.
func (client *Client) AddImportListExclusion(title string, tvdbID int) error {
	if tvdbID <= 0 {
		return fmt.Errorf("sonarr import-list exclusion requires a TVDB id")
	}
	body := struct {
		TVDBID int    `json:"tvdbId"`
		Title  string `json:"title"`
	}{TVDBID: tvdbID, Title: title}
	err := client.Send(http.MethodPost, "/api/v3/importlistexclusion", body, nil)
	if arr.ExclusionAlreadyAdded(err) {
		return nil
	}
	return err
}

// Exclusions are the series of Sonarr's import-list exclusions, by TVDB
// ID: what the operator doesn't want added.
func (client *Client) Exclusions() (map[int]bool, error) {
	var list []struct {
		TVDBID int `json:"tvdbId"`
	}
	if err := client.Send(http.MethodGet, "/api/v3/importlistexclusion", nil, &list); err != nil {
		return nil, err
	}
	out := map[int]bool{}
	for _, x := range list {
		if x.TVDBID > 0 {
			out[x.TVDBID] = true
		}
	}
	return out, nil
}
