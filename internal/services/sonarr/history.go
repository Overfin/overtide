package sonarr

import (
	"time"

	"github.com/overfin/overtide/internal/services/arr"
)

type ImportEvent struct {
	SeriesID   int
	EpisodeID  int
	DownloadID string
	Date       time.Time
}

// ImportEventsSince returns the episodes' imports since then, newest first
// (see arr.ImportsSince), and the newest record's date.
func (client *Client) ImportEventsSince(since time.Time) ([]ImportEvent, time.Time, error) {
	var out []ImportEvent
	newest, err := client.ImportsSince(since, func(r arr.HistoryRecord, date time.Time) {
		if r.SeriesID > 0 && r.EpisodeID > 0 {
			out = append(out, ImportEvent{SeriesID: r.SeriesID, EpisodeID: r.EpisodeID, DownloadID: r.DownloadID, Date: date})
		}
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	return out, newest, nil
}
