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
	// The import itself, for History: Sonarr's record, the episode, the
	// file and what the release was called.
	RecordID    int
	Season      int
	Episode     int
	Path        string
	SizeBytes   int64
	SourceTitle string
}

// ImportEventsSince returns the episodes' imports since then, newest first
// (see arr.ImportsSince), and the newest record's date.
func (client *Client) ImportEventsSince(since time.Time) ([]ImportEvent, time.Time, error) {
	var out []ImportEvent
	newest, err := client.ImportsSince(since, func(r arr.HistoryRecord, date time.Time) {
		if r.SeriesID > 0 && r.EpisodeID > 0 {
			out = append(out, ImportEvent{SeriesID: r.SeriesID, EpisodeID: r.EpisodeID, DownloadID: r.DownloadID, Date: date,
				RecordID: r.ID, Season: r.Episode.SeasonNumber, Episode: r.Episode.EpisodeNumber, Path: r.Data.ImportedPath, SizeBytes: r.SizeBytes(), SourceTitle: r.SourceTitle})
		}
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	return out, newest, nil
}
