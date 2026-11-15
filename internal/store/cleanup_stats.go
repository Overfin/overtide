package store

type CleanupStats struct {
	Runs            int64
	MediaRemoved    int64
	TorrentsRemoved int64
	MediaBytes      int64
	ReclaimedBytes  int64
	Last30Runs      int64
	Last30Media     int64
	Last30Bytes     int64
}

func (s *Store) CleanupStatistics() (CleanupStats, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	var out CleanupStats
	err := s.q.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN requested_kind IN ('media','season','bundle') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN requested_kind='torrent' THEN 1 ELSE 0 END),0),
		COALESCE(SUM(media_bytes),0), COALESCE(SUM(reclaimable_bytes),0)
		FROM history_events WHERE event_type='removal' AND status='success'`).
		Scan(&out.Runs, &out.MediaRemoved, &out.TorrentsRemoved, &out.MediaBytes, &out.ReclaimedBytes)
	if err != nil {
		return out, err
	}
	err = s.q.QueryRow(`SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN requested_kind IN ('media','season','bundle') THEN 1 ELSE 0 END),0),
		COALESCE(SUM(reclaimable_bytes),0)
		FROM history_events WHERE event_type='removal' AND status='success' AND created_at >= datetime('now','-30 days')`).
		Scan(&out.Last30Runs, &out.Last30Media, &out.Last30Bytes)
	return out, err
}
