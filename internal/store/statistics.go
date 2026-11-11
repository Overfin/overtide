package store

import "time"

// Statistics over time, for the Statistics page: what came (the files
// Radarr and Sonarr imported) and what went (the removals), counted by
// period, and each disk's use, sampled once a day.

// Period is how statistics are grouped: by day, week (from Monday) or
// month.
type Period string

const (
	ByDay   Period = "day"
	ByWeek  Period = "week"
	ByMonth Period = "month"
)

// periodOf is the SQL that turns a date (YYYY-MM-DD…) into its period's
// first day.
func periodOf(period Period, date string) string {
	switch period {
	case ByWeek:
		return `date(` + date + `, 'weekday 0', '-6 days')`
	case ByMonth:
		return `date(` + date + `, 'start of month')`
	}
	return `date(` + date + `)`
}

// ImportCount is what was imported in a period.
type ImportCount struct {
	Start         string // its first day, YYYY-MM-DD
	Films         int64
	Episodes      int64
	FilmBytes     int64
	EpisodeBytes  int64
	ByOvertide    int64 // files Overtide brought (the others: Radarr's and Sonarr's own)
	OvertideBytes int64
}

// ImportsSince counts the files imported from the day since (YYYY-MM-DD),
// by period.
func (s *Store) ImportsSince(since string, period Period) ([]ImportCount, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	start := periodOf(period, "day")
	rows, err := s.q.Query(`SELECT `+start+` AS p,
		SUM(kind = 'movie'), SUM(kind <> 'movie'),
		COALESCE(SUM(CASE WHEN kind = 'movie' THEN size END), 0), COALESCE(SUM(CASE WHEN kind <> 'movie' THEN size END), 0),
		SUM(origin = 'adding'), COALESCE(SUM(CASE WHEN origin = 'adding' THEN size END), 0)
		FROM imported_files WHERE day >= ? GROUP BY p ORDER BY p`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportCount
	for rows.Next() {
		var c ImportCount
		if err := rows.Scan(&c.Start, &c.Films, &c.Episodes, &c.FilmBytes, &c.EpisodeBytes, &c.ByOvertide, &c.OvertideBytes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RemovalCount is what was removed in a period (removals done, not dry
// runs).
type RemovalCount struct {
	Start      string
	Titles     int64 // films, series, seasons
	Torrents   int64
	Other      int64 // unmanaged files
	FreedBytes int64 // the space the removals gave back
}

// RemovalsSince counts the removals from the day since, by period.
func (s *Store) RemovalsSince(since string, period Period) ([]RemovalCount, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	start := periodOf(period, "substr(created_at, 1, 10)")
	rows, err := s.q.Query(`SELECT `+start+` AS p,
		SUM(requested_kind IN ('media', 'season', 'bundle')), SUM(requested_kind = 'torrent'),
		SUM(requested_kind NOT IN ('media', 'season', 'bundle', 'torrent')), COALESCE(SUM(reclaimable_bytes), 0)
		FROM history_events
		WHERE event_type = 'removal' AND status IN ('success', 'partial') AND dry_run = 0 AND substr(created_at, 1, 10) >= ?
		GROUP BY p ORDER BY p`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RemovalCount
	for rows.Next() {
		var c RemovalCount
		if err := rows.Scan(&c.Start, &c.Titles, &c.Torrents, &c.Other, &c.FreedBytes); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// FirstStatisticsDay is the first day statistics have anything for:
// the oldest import or removal; "" without any.
func (s *Store) FirstStatisticsDay() (string, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	var day string
	err := s.q.QueryRow(`SELECT COALESCE(MIN(d), '') FROM (
		SELECT MIN(day) AS d FROM imported_files
		UNION ALL SELECT MIN(substr(created_at, 1, 10)) FROM history_events WHERE event_type = 'removal' AND dry_run = 0)`).Scan(&day)
	return day, err
}

// DiskSample is a disk's use on a day: the whole disk's, and Overtide's
// services' part of it.
type DiskSample struct {
	Day       string
	Device    string
	UsedBytes int64
	// ServicesBytes is what the services' files take (Overtide's used).
	ServicesBytes int64
	TotalBytes    int64
}

// KeepDiskSample keeps a disk's use for its day: the day's last sample
// stays.
func (s *Store) KeepDiskSample(sample DiskSample) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.exec(`INSERT INTO disk_samples (day, device, used_bytes, services_bytes, total_bytes) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (day, device) DO UPDATE SET used_bytes = excluded.used_bytes, services_bytes = excluded.services_bytes,
			total_bytes = excluded.total_bytes`,
		sample.Day, sample.Device, sample.UsedBytes, sample.ServicesBytes, sample.TotalBytes)
}

// DiskSamplesSince lists the disks' daily samples from the day since, by
// day.
func (s *Store) DiskSamplesSince(since string) ([]DiskSample, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	rows, err := s.q.Query(`SELECT day, device, used_bytes, services_bytes, total_bytes FROM disk_samples
		WHERE day >= ? ORDER BY day, device`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DiskSample
	for rows.Next() {
		var d DiskSample
		if err := rows.Scan(&d.Day, &d.Device, &d.UsedBytes, &d.ServicesBytes, &d.TotalBytes); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Today is a time's day as statistics count them (UTC).
func Today(t time.Time) string { return t.UTC().Format(time.DateOnly) }

// ImportTotals counts the files imported from the day since (YYYY-MM-DD,
// "" for all): how many, and their size.
func (s *Store) ImportTotals(since string) (files, bytes int64, err error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	err = s.q.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM imported_files WHERE day >= ?`, since).Scan(&files, &bytes)
	return files, bytes, err
}
