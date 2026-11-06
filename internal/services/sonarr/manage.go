package sonarr

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Title is a series: its folder and size, its tags, whether Sonarr looks
// for it, and when it was added.
type Title struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Year   int    `json:"year"`
	Path   string `json:"path"`
	TVDBID int    `json:"tvdbId"`
	// TMDBID is Sonarr's own TMDB ID for the series (Sonarr 4); Overfin
	// knows some series by it alone.
	TMDBID     int    `json:"tmdbId"`
	IMDBID     string `json:"imdbId"`
	Statistics struct {
		SizeOnDisk int64 `json:"sizeOnDisk"`
	} `json:"statistics"`
	// SeriesType is standard, daily or anime.
	SeriesType string    `json:"seriesType"`
	Tags       []int     `json:"tags"`
	Monitored  bool      `json:"monitored"`
	Added      time.Time `json:"added"`
	// MonitorNewItems is "all" when Sonarr monitors new seasons itself.
	MonitorNewItems string `json:"monitorNewItems"`
	Seasons         []struct {
		SeasonNumber int  `json:"seasonNumber"`
		Monitored    bool `json:"monitored"`
		Statistics   struct {
			EpisodeFileCount  int `json:"episodeFileCount"`
			TotalEpisodeCount int `json:"totalEpisodeCount"`
		} `json:"statistics"`
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
	return titles, client.Get("/api/v3/series", &titles)
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
	return client.Send(http.MethodPut, "/api/v3/series/editor", map[string]any{"seriesIds": ids, "tags": []int{tagID}, "applyTags": apply}, nil)
}

// Episode is one episode: whether it has its file, whether Sonarr looks
// for it, and when it airs.
type Episode struct {
	ID            int `json:"id"`
	SeriesID      int `json:"seriesId"`
	SeasonNumber  int `json:"seasonNumber"`
	EpisodeNumber int `json:"episodeNumber"`
	// AbsoluteNumber is its number through the whole series, for anime.
	AbsoluteNumber int       `json:"absoluteEpisodeNumber"`
	Title          string    `json:"title"`
	HasFile        bool      `json:"hasFile"`
	EpisodeFileID  int       `json:"episodeFileId"`
	Monitored      bool      `json:"monitored"`
	AirDateUTC     time.Time `json:"airDateUtc"`
}

// Episodes returns a series' episodes.
func (client *Client) Episodes(seriesID int) ([]Episode, error) {
	var episodes []Episode
	return episodes, client.Get(fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID), &episodes)
}

// Calendar returns the episodes airing between start and end, monitored
// or not.
func (client *Client) Calendar(start, end time.Time) ([]Episode, error) {
	query := url.Values{"start": {start.UTC().Format(time.RFC3339)}, "end": {end.UTC().Format(time.RFC3339)}, "unmonitored": {"true"}}
	var episodes []Episode
	return episodes, client.Get("/api/v3/calendar?"+query.Encode(), &episodes)
}

// SearchEpisodes asks Sonarr to search for these episodes, and returns the
// command's ID to follow it. Sonarr's episode search doesn't look at
// monitoring: the episodes needn't be monitored.
func (client *Client) SearchEpisodes(ids []int) (int, error) {
	return client.Command(map[string]any{"name": "EpisodeSearch", "episodeIds": ids})
}

// TakeSeries leaves Sonarr nothing to grab on its own for the series:
// every season unmonitored (Sonarr carries it to their episodes), and new
// seasons not monitored when they appear. The series stays monitored, so
// the episodes Overtide monitors are grabbed.
func (client *Client) TakeSeries(seriesID int) error {
	return client.updateSeries(seriesID, func(series map[string]any) {
		seasons, _ := series["seasons"].([]any)
		for _, raw := range seasons {
			if season, ok := raw.(map[string]any); ok {
				season["monitored"] = false
			}
		}
		series["monitored"] = true
		series["monitorNewItems"] = "none"
	})
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
	if err := client.Get("/api/v3/wanted/missing?"+query.Encode(), &missing); err != nil {
		return nil, 0, err
	}
	return missing.Records, missing.TotalRecords, nil
}

// GetEpisode returns one episode. One no longer in Sonarr gives an error
// arr.IsNotFound recognizes.
func (client *Client) GetEpisode(id int) (Episode, error) {
	var episode Episode
	return episode, client.Get(fmt.Sprintf("/api/v3/episode/%d", id), &episode)
}

// QueuedEpisodes returns the IDs of the episodes with a download in
// Sonarr's queue.
func (client *Client) QueuedEpisodes() (map[int]bool, error) {
	queued, err := client.FullQueue()
	if err != nil {
		return nil, err
	}
	ids := make(map[int]bool, len(queued))
	for _, q := range queued {
		ids[q.EpisodeID] = true
	}
	return ids, nil
}

// SeriesPath returns a series' folder.
func (client *Client) SeriesPath(seriesID int) (string, error) {
	var series struct {
		Path string `json:"path"`
	}
	return series.Path, client.Get(fmt.Sprintf("/api/v3/series/%d", seriesID), &series)
}

// HistoryEvent is one event of a series' history.
type HistoryEvent struct {
	EpisodeID  int               `json:"episodeId"`
	EventType  string            `json:"eventType"`
	Date       time.Time         `json:"date"`
	DownloadID string            `json:"downloadId"`
	Data       map[string]string `json:"data"`
}

// History returns a series' history.
func (client *Client) History(seriesID int) ([]HistoryEvent, error) {
	var events []HistoryEvent
	return events, client.Get(fmt.Sprintf("/api/v3/history/series?seriesId=%d", seriesID), &events)
}

// DeletedSince returns the episodes that lost their file since then, an
// upgrade replacing it aside: someone or something removed them.
func DeletedSince(events []HistoryEvent, since time.Time) map[int]bool {
	episodes := map[int]bool{}
	for _, event := range events {
		if event.EventType == "episodeFileDeleted" && event.Date.After(since) && !strings.EqualFold(event.Data["reason"], "Upgrade") {
			episodes[event.EpisodeID] = true
		}
	}
	return episodes
}

// PacksImported returns, by episode, when a whole-season pack it was part of
// was grabbed, for the packs Sonarr imported something of. The latest pack
// wins.
func PacksImported(events []HistoryEvent) map[int]time.Time {
	grabbed := map[string]time.Time{}
	for _, event := range events {
		if event.EventType == "grabbed" && event.DownloadID != "" && strings.EqualFold(event.Data["releaseType"], "SeasonPack") && event.Date.After(grabbed[event.DownloadID]) {
			grabbed[event.DownloadID] = event.Date
		}
	}
	episodes := map[int]time.Time{}
	for _, event := range events {
		if event.EventType != "downloadFolderImported" {
			continue
		}
		if at, ok := grabbed[event.DownloadID]; ok && at.After(episodes[event.EpisodeID]) {
			episodes[event.EpisodeID] = at
		}
	}
	return episodes
}

// ForgetDownloads takes items off Sonarr's queue without touching the
// download client: the download stays, and goes on seeding.
func (client *Client) ForgetDownloads(ids []int) error {
	query := url.Values{"removeFromClient": {"false"}, "blocklist": {"false"}, "skipRedownload": {"true"}}
	return client.Send(http.MethodDelete, "/api/v3/queue/bulk?"+query.Encode(), map[string]any{"ids": ids}, nil)
}

// cutoffPages bounds how many pages of the cutoff list are read (1000 each).
const cutoffPages = 200

// SeasonRef is a season of a series.
type SeasonRef struct {
	SeriesID int
	Season   int
}

// BelowCutoff returns the seasons with an unmonitored episode below its
// quality profile's cutoff. The list compares qualities only, not custom
// format scores.
func (client *Client) BelowCutoff() ([]SeasonRef, error) {
	seen := map[SeasonRef]bool{}
	var out []SeasonRef
	for page := 1; page <= cutoffPages; page++ {
		query := url.Values{"page": {fmt.Sprint(page)}, "pageSize": {"1000"}, "monitored": {"false"}}
		var list struct {
			TotalRecords int `json:"totalRecords"`
			Records      []struct {
				SeriesID     int `json:"seriesId"`
				SeasonNumber int `json:"seasonNumber"`
			} `json:"records"`
		}
		if err := client.Get("/api/v3/wanted/cutoff?"+query.Encode(), &list); err != nil {
			return nil, err
		}
		for _, r := range list.Records {
			ref := SeasonRef{r.SeriesID, r.SeasonNumber}
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
		if len(list.Records) == 0 || page*1000 >= list.TotalRecords {
			break
		}
	}
	return out, nil
}
