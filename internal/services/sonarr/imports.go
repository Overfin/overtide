package sonarr

import (
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/overfin/overtide/internal/services/arr"
)

// AutoImportOn reports whether Sonarr imports finished downloads itself
// (its Completed Download Handling). With it off, Overtide does. A Sonarr
// that doesn't say is taken to do so: only a clear "off" hands imports over.
func (client *Client) AutoImportOn() (bool, error) {
	var settings struct {
		On *bool `json:"enableCompletedDownloadHandling"`
	}
	if err := client.Get("/api/v3/config/downloadclient", &settings); err != nil {
		return true, err
	}
	return settings.On == nil || *settings.On, nil
}

// Grabbed is a download Sonarr started: one event per episode it counts on.
type Grabbed struct {
	DownloadID  string    `json:"downloadId"`
	SeriesID    int       `json:"seriesId"`
	SourceTitle string    `json:"sourceTitle"`
	Date        time.Time `json:"date"`
}

// GrabsSince returns the downloads Sonarr started since then, however they
// were started: its own searches and RSS, or by hand.
func (client *Client) GrabsSince(since time.Time) ([]Grabbed, error) {
	query := url.Values{"date": {since.UTC().Format(time.RFC3339)}, "eventType": {"grabbed"}}
	var grabs []Grabbed
	return grabs, client.Get("/api/v3/history/since?"+query.Encode(), &grabs)
}

// historyPage is how many of a download's events are read: one per episode
// per event, so a long season's fit.
const historyPage = 1000

// DownloadEvents returns a download's history.
func (client *Client) DownloadEvents(downloadID string) ([]HistoryEvent, error) {
	query := url.Values{"downloadId": {downloadID}, "pageSize": {strconv.Itoa(historyPage)}}
	var page struct {
		Records []HistoryEvent `json:"records"`
	}
	return page.Records, client.Get("/api/v3/history?"+query.Encode(), &page)
}

// DownloadFiles returns a finished download's own files. With a series ID,
// Sonarr would list the series' folder instead, so none is given. Sonarr has
// no list for a download still going: it errors, or answers none.
func (client *Client) DownloadFiles(downloadID string) ([]ImportItem, error) {
	query := url.Values{"downloadId": {downloadID}, "filterExistingFiles": {"false"}}
	var items []ImportItem
	return items, client.Patient().Get("/api/v3/manualimport?"+query.Encode(), &items)
}

// StillGoing reports whether an error from DownloadFiles is Sonarr's answer
// about a download with no list yet, rather than Sonarr not answering.
func StillGoing(err error) bool {
	var status *arr.StatusError
	return errors.As(err, &status)
}
