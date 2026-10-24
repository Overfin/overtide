package store

import (
	"time"
)

// TorrentHistorySample is one periodic reading for one torrent. Unlike the
// live model.Torrent snapshot, these accumulate over time so sustained
// patterns (real contribution, consistent activity) can be told apart from
// a single misleading instantaneous reading.
type TorrentHistorySample struct {
	Client          string
	Hash            string
	SampledAt       time.Time
	Ratio           float64
	SeedsSwarm      int
	LeechersSwarm   int
	UploadedBytes   int64
	DownloadedBytes int64
	State           string
	LastActivity    int64
}

// SaveTorrentHistorySamples appends one row per sample. Existing rows are
// never modified or replaced; only PruneTorrentHistory removes rows, and
// only by age.
func (s *Store) SaveTorrentHistorySamples(samples []TorrentHistorySample) error {
	if len(samples) == 0 {
		return nil
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	rows := make([][]any, 0, len(samples))
	for _, x := range samples {
		rows = append(rows, []any{x.Client, x.Hash, rfc3339(x.SampledAt), x.Ratio, x.SeedsSwarm, x.LeechersSwarm,
			x.UploadedBytes, x.DownloadedBytes, x.State, x.LastActivity})
	}
	return s.withWriteTx(func() error {
		return s.insertAll(`INSERT INTO torrent_history(client,hash,sampled_at,ratio,seeds_swarm,leechers_swarm,uploaded_bytes,downloaded_bytes,state,last_activity) VALUES(?,?,?,?,?,?,?,?,?,?)`, rows)
	})
}

// PruneTorrentHistory deletes every sample older than before, so the table
// never grows unbounded. Called once per sampling cycle, right after
// SaveTorrentHistorySamples.
func (s *Store) PruneTorrentHistory(before time.Time) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		return s.exec(`DELETE FROM torrent_history WHERE sampled_at < ?`, rfc3339(before))
	})
}

// historySamples reads samples, each row client, hash, then the sample's
// own columns.
func (s *Store) historySamples(query string, args ...any) ([]TorrentHistorySample, error) {
	rows, err := s.q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TorrentHistorySample
	for rows.Next() {
		var x TorrentHistorySample
		var at string
		if err := rows.Scan(&x.Client, &x.Hash, &at, &x.Ratio, &x.SeedsSwarm, &x.LeechersSwarm,
			&x.UploadedBytes, &x.DownloadedBytes, &x.State, &x.LastActivity); err != nil {
			return nil, err
		}
		x.SampledAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, x)
	}
	return out, rows.Err()
}

const sampleColumns = `client,hash,sampled_at,ratio,seeds_swarm,leechers_swarm,uploaded_bytes,downloaded_bytes,state,last_activity`

// TorrentHistorySince returns every retained sample at or after since,
// across every torrent, in one query — grouped by client+hash so a caller
// scoring many torrents at once (e.g. valuation.ApplyTorrentValue) never
// needs one query per torrent. Each torrent's samples are oldest first.
func (s *Store) TorrentHistorySince(since time.Time) (map[string][]TorrentHistorySample, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	samples, err := s.historySamples(`SELECT `+sampleColumns+` FROM torrent_history WHERE sampled_at >= ? ORDER BY client,hash,sampled_at ASC`, rfc3339(since))
	if err != nil {
		return nil, err
	}
	out := map[string][]TorrentHistorySample{}
	for _, x := range samples {
		key := x.Client + "|" + x.Hash
		out[key] = append(out[key], x)
	}
	return out, nil
}

// TorrentHistorySamples returns every retained sample for one torrent,
// oldest first.
func (s *Store) TorrentHistorySamples(client, hash string) ([]TorrentHistorySample, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	return s.historySamples(`SELECT `+sampleColumns+` FROM torrent_history WHERE client=? AND hash=? ORDER BY sampled_at ASC`, client, hash)
}
