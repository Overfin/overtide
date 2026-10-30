package sonarr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Release is one release an interactive search found.
type Release struct {
	GUID            string   `json:"guid"`
	IndexerID       int      `json:"indexerId"`
	Title           string   `json:"title"`
	FullSeason      bool     `json:"fullSeason"`
	SeasonNumber    int      `json:"seasonNumber"`
	Rejections      []string `json:"rejections"`
	DownloadAllowed bool     `json:"downloadAllowed"`
}

// searchTimeout bounds an interactive search, which asks every indexer.
const searchTimeout = 3 * time.Minute

// SeasonReleases asks the indexers for a season, as Sonarr's interactive
// search does, and returns every release found, refused ones with why, in
// Sonarr's order of preference.
func (client *Client) SeasonReleases(seriesID, season int) ([]Release, error) {
	slow := *client
	hc := *client.hc
	hc.Timeout = searchTimeout
	slow.hc = &hc
	var releases []Release
	return releases, slow.send(http.MethodGet, fmt.Sprintf("/api/v3/release?seriesId=%d&seasonNumber=%d", seriesID, season), nil, &releases)
}

// GrabRelease sends a release an interactive search found to the download
// client, whatever Sonarr's own decision on it was.
func (client *Client) GrabRelease(release Release) error {
	return client.send(http.MethodPost, "/api/v3/release", map[string]any{"guid": release.GUID, "indexerId": release.IndexerID}, nil)
}

// QueueItem is a download in Sonarr's queue.
type QueueItem struct {
	DownloadID   string `json:"downloadId"`
	Title        string `json:"title"`
	SeriesID     int    `json:"seriesId"`
	SeasonNumber int    `json:"seasonNumber"`
	// Status is the download's (queued, downloading, completed, failed…);
	// TrackedDownloadState is Sonarr's import of it (importPending,
	// importBlocked, imported, failed…).
	Status               string `json:"status"`
	TrackedDownloadState string `json:"trackedDownloadState"`
}

// Queue returns a series' downloads in Sonarr's queue, one item per
// episode.
func (client *Client) Queue(seriesID int) ([]QueueItem, error) {
	var items []QueueItem
	return items, client.send(http.MethodGet, fmt.Sprintf("/api/v3/queue/details?seriesId=%d", seriesID), nil, &items)
}

// ImportItem is a file of a download, as Sonarr's manual import sees it.
type ImportItem struct {
	Path         string `json:"path"`
	FolderName   string `json:"folderName"`
	SeasonNumber *int   `json:"seasonNumber"`
	Episodes     []struct {
		ID int `json:"id"`
	} `json:"episodes"`
	ReleaseGroup string          `json:"releaseGroup"`
	Quality      json.RawMessage `json:"quality"`
	Languages    json.RawMessage `json:"languages"`
	IndexerFlags int             `json:"indexerFlags"`
	ReleaseType  json.RawMessage `json:"releaseType"`
	DownloadID   string          `json:"downloadId"`
}

// ImportItems returns the files of a download, imported already or not.
func (client *Client) ImportItems(downloadID string, seriesID int) ([]ImportItem, error) {
	query := url.Values{"downloadId": {downloadID}, "seriesId": {fmt.Sprint(seriesID)}, "filterExistingFiles": {"false"}}
	var items []ImportItem
	return items, client.send(http.MethodGet, "/api/v3/manualimport?"+query.Encode(), nil, &items)
}

// Import imports the files as Sonarr's manual import does: whatever it
// thinks of them, replacing the episodes' files (into its recycle bin, if
// it has one). It returns the command's ID.
func (client *Client) Import(seriesID int, items []ImportItem) (int, error) {
	files := make([]map[string]any, 0, len(items))
	for _, item := range items {
		ids := make([]int, 0, len(item.Episodes))
		for _, e := range item.Episodes {
			ids = append(ids, e.ID)
		}
		files = append(files, map[string]any{"path": item.Path, "folderName": item.FolderName, "seriesId": seriesID, "episodeIds": ids,
			"quality": item.Quality, "languages": item.Languages, "releaseGroup": item.ReleaseGroup, "indexerFlags": item.IndexerFlags,
			"releaseType": item.ReleaseType, "downloadId": item.DownloadID})
	}
	var command struct {
		ID int `json:"id"`
	}
	return command.ID, client.send(http.MethodPost, "/api/v3/command", map[string]any{"name": "ManualImport", "files": files, "importMode": "auto"}, &command)
}

// EpisodeFile is an episode file: its season and when it came.
type EpisodeFile struct {
	ID           int       `json:"id"`
	SeasonNumber int       `json:"seasonNumber"`
	DateAdded    time.Time `json:"dateAdded"`
}

// EpisodeFiles returns a series' episode files.
func (client *Client) EpisodeFiles(seriesID int) ([]EpisodeFile, error) {
	var files []EpisodeFile
	return files, client.send(http.MethodGet, fmt.Sprintf("/api/v3/episodefile?seriesId=%d", seriesID), nil, &files)
}
