package radarr

import (
	"time"

	"github.com/overfin/overtide/internal/services/arr"
)

type ImportEvent struct {
	MovieID    int
	DownloadID string
	Date       time.Time
}

// ImportEventsSince returns the films' imports since then, newest first
// (see arr.ImportsSince), and the newest record's date.
func (client *Client) ImportEventsSince(since time.Time) ([]ImportEvent, time.Time, error) {
	var out []ImportEvent
	newest, err := client.ImportsSince(since, func(r arr.HistoryRecord, date time.Time) {
		if r.MovieID > 0 {
			out = append(out, ImportEvent{MovieID: r.MovieID, DownloadID: r.DownloadID, Date: date})
		}
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	return out, newest, nil
}
