package store

import "time"

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

type CleanupRun struct {
	ID              int64
	StartedAt       time.Time
	CompletedAt     time.Time
	UsageBefore     float64
	UsageAfter      float64
	TargetUsage     float64
	CriticalUsage   float64
	PlannedBytes    int64
	MediaBytes      int64
	ReclaimedBytes  int64
	MediaRemoved    int64
	TorrentsRemoved int64
	Status          string
	Reason          string
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

func (s *Store) CleanupRuns(limit int) ([]CleanupRun, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.Query(`SELECT id,started_at,completed_at,usage_before,usage_after,target_usage,critical_usage,planned_bytes,media_bytes,reclaimed_bytes,media_removed,torrents_removed,status,reason FROM cleanup_runs ORDER BY completed_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CleanupRun
	for rows.Next() {
		var r CleanupRun
		var started, completed string
		if err := rows.Scan(&r.ID, &started, &completed, &r.UsageBefore, &r.UsageAfter, &r.TargetUsage, &r.CriticalUsage,
			&r.PlannedBytes, &r.MediaBytes, &r.ReclaimedBytes, &r.MediaRemoved, &r.TorrentsRemoved, &r.Status, &r.Reason); err != nil {
			return nil, err
		}
		r.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
		r.CompletedAt, _ = time.Parse(time.RFC3339Nano, completed)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RecordCleanupRun(r CleanupRun) (int64, error) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	res, err := s.q.Exec(`INSERT INTO cleanup_runs(started_at,completed_at,usage_before,usage_after,target_usage,critical_usage,planned_bytes,media_bytes,reclaimed_bytes,media_removed,torrents_removed,status,reason) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rfc3339(r.StartedAt), rfc3339(r.CompletedAt), r.UsageBefore, r.UsageAfter, r.TargetUsage, r.CriticalUsage,
		r.PlannedBytes, r.MediaBytes, r.ReclaimedBytes, r.MediaRemoved, r.TorrentsRemoved, r.Status, r.Reason)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
