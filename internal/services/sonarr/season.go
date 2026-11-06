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
	// Approved: Sonarr would take it as it is.
	Approved bool `json:"approved"`
	// Episodes are the episodes Sonarr matched it to.
	Episodes []ReleaseEpisode `json:"mappedEpisodeInfo"`
	// DownloadURL is its .torrent's address: it may hold a tracker's key, so
	// it's never logged.
	DownloadURL string `json:"downloadUrl"`
}

// ReleaseEpisode is an episode a release was matched to.
type ReleaseEpisode struct {
	ID int `json:"id"`
}

// SeasonReleases asks the indexers for a season, as Sonarr's interactive
// search does, and returns every release found, refused ones with why, in
// Sonarr's order of preference.
func (client *Client) SeasonReleases(seriesID, season int) ([]Release, error) {
	var releases []Release
	return releases, client.Patient().Get(fmt.Sprintf("/api/v3/release?seriesId=%d&seasonNumber=%d", seriesID, season), &releases)
}

// GrabRelease sends a release an interactive search found to the download
// client, whatever Sonarr's own decision on it was.
func (client *Client) GrabRelease(release Release) error {
	return client.Send(http.MethodPost, "/api/v3/release", map[string]any{"guid": release.GUID, "indexerId": release.IndexerID}, nil)
}

// QueueItem is an episode of a download in Sonarr's queue.
type QueueItem struct {
	// ID is the queue item's, for ForgetDownloads.
	ID         int    `json:"id"`
	DownloadID string `json:"downloadId"`
	Title      string `json:"title"`
	SeriesID   int    `json:"seriesId"`
	EpisodeID  int    `json:"episodeId"`
	// SeasonNumber is the episode's season, and Added when the download
	// joined the queue.
	SeasonNumber int       `json:"seasonNumber"`
	Added        time.Time `json:"added"`
	// Status is the download's (queued, downloading, completed, failed…);
	// TrackedDownloadState is Sonarr's import of it (importPending,
	// importBlocked, imported, failed…); TrackedDownloadStatus, "warning"
	// once Sonarr tried and refused some of it. The queue's own
	// episodeHasFile isn't to be relied on: it says false of episodes that
	// have their files.
	Status                string `json:"status"`
	TrackedDownloadState  string `json:"trackedDownloadState"`
	TrackedDownloadStatus string `json:"trackedDownloadStatus"`
	// StatusMessages are why Sonarr refused files: Title is the file.
	StatusMessages []struct {
		Title    string   `json:"title"`
		Messages []string `json:"messages"`
	} `json:"statusMessages"`
	// Size and SizeLeft are in bytes; EstimatedCompletion is when the
	// download client thinks it's done, nil when it doesn't say.
	Size                float64    `json:"size"`
	SizeLeft            float64    `json:"sizeleft"`
	EstimatedCompletion *time.Time `json:"estimatedCompletionTime"`
}

// FullQueue returns every download in Sonarr's queue, one item per
// episode.
func (client *Client) FullQueue() ([]QueueItem, error) {
	var items []QueueItem
	return items, client.Get("/api/v3/queue/details", &items)
}

// Queue returns a series' downloads in Sonarr's queue, one item per
// episode.
func (client *Client) Queue(seriesID int) ([]QueueItem, error) {
	var items []QueueItem
	return items, client.Get(fmt.Sprintf("/api/v3/queue/details?seriesId=%d", seriesID), &items)
}

// ImportItem is a file of a download, as Sonarr's manual import sees it.
type ImportItem struct {
	Path         string `json:"path"`
	FolderName   string `json:"folderName"`
	SeasonNumber *int   `json:"seasonNumber"`
	// Episodes are what Sonarr made of the file's name: a guess.
	Episodes []ImportEpisode `json:"episodes"`
	// Rejections are why Sonarr's own import would refuse it.
	Rejections   []ImportRejection `json:"rejections"`
	ReleaseGroup string            `json:"releaseGroup"`
	Quality      json.RawMessage   `json:"quality"`
	Languages    json.RawMessage   `json:"languages"`
	IndexerFlags int               `json:"indexerFlags"`
	ReleaseType  json.RawMessage   `json:"releaseType"`
	DownloadID   string            `json:"downloadId"`
	// ByAbsolute: Overtide placed it by its absolute number.
	ByAbsolute bool `json:"-"`
}

// ImportEpisode is an episode Sonarr's manual import has a file on.
type ImportEpisode struct {
	ID int `json:"id"`
}

// ImportRejection is why Sonarr's own import would refuse a file.
type ImportRejection struct {
	Reason string `json:"reason"`
}

// ImportItems returns the files of a download, imported already or not.
func (client *Client) ImportItems(downloadID string, seriesID int) ([]ImportItem, error) {
	query := url.Values{"downloadId": {downloadID}, "seriesId": {fmt.Sprint(seriesID)}, "filterExistingFiles": {"false"}}
	var items []ImportItem
	return items, client.Get("/api/v3/manualimport?"+query.Encode(), &items)
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
	return command.ID, client.Send(http.MethodPost, "/api/v3/command", map[string]any{"name": "ManualImport", "files": files, "importMode": "auto"}, &command)
}
