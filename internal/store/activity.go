package store

import (
	"strconv"
	"strings"
	"time"
)

// ImportedFile is one file Radarr or Sonarr imported, as History keeps it:
// with the title's name at the time, so it still reads once the title is
// gone.
type ImportedFile struct {
	ServiceID   string
	ServiceName string
	// RecordID is the app's history record: with ServiceID, the file's
	// identity, so a record read twice is kept once.
	RecordID int
	Kind     string
	SourceID int
	Title    string
	Year     int
	Season   int
	Episode  int
	Path     string
	Size     int64
	// Origin is what brought the title: "adding", "wish", or "" (from
	// outside Overtide).
	Origin      string
	SourceTitle string
	ImportedAt  time.Time
}

// AddImportedFiles keeps the files, each once.
func (s *Store) AddImportedFiles(files []ImportedFile) error {
	if len(files) == 0 {
		return nil
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	rows := make([][]any, 0, len(files))
	for _, f := range files {
		rows = append(rows, []any{f.ServiceID, f.RecordID, f.ServiceName, f.Kind, f.SourceID, f.Title, f.Year, f.Season, f.Episode, f.Path, f.Size,
			f.Origin, f.SourceTitle, rfc3339(f.ImportedAt), f.ImportedAt.In(time.Local).Format(time.DateOnly)})
	}
	return s.withWriteTx(func() error {
		return s.insertAll(`INSERT OR IGNORE INTO imported_files(service_id,record_id,service_name,kind,source_id,title,year,season,episode,path,size,origin,source_title,imported_at,day) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, rows)
	})
}

// Activity is one row of History: a title's imports on one day, or one
// removal.
type Activity struct {
	// Action is "added" or "removed".
	Action      string
	At          time.Time
	Day         string
	ServiceID   string
	ServiceName string
	Kind        string
	SourceID    int
	Title       string
	Year        int
	Files       int
	Bytes       int64
	// Episodes are an added series' episodes, as [season, episode].
	Episodes [][2]int
	Origin   string
	// A removal's status, error and operation.
	Status      string
	Error       string
	OperationID int64
}

// ActivityQuery picks History's rows.
type ActivityQuery struct {
	Since time.Time
	// Show is "added", "removed", "failed", or "" for all.
	Show   string
	Search string
	Limit  int
	Offset int
}

// activityRows are imports, one per title and day, and live removals.
const activityRows = `SELECT 'added' AS action, MAX(imported_at) AS at, day, service_id, MAX(service_name) AS service_name, kind, source_id, MAX(title) AS title, MAX(year) AS year,
		COUNT(*) AS files, SUM(size) AS bytes, GROUP_CONCAT(season || ':' || episode) AS episodes, MAX(origin) AS origin, '' AS status, '' AS error, 0 AS operation
	FROM imported_files WHERE imported_at >= ? GROUP BY service_id, kind, source_id, day
	UNION ALL
	SELECT 'removed', created_at, '', '', service_name, requested_kind, 0, requested_label, 0, 0, reclaimable_bytes, '', '', status, error, id
	FROM history_events WHERE event_type='removal' AND dry_run=0 AND created_at >= ?`

// Activities returns History's rows, the newest first, and how many there
// are in all.
func (s *Store) Activities(q ActivityQuery) ([]Activity, int, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	since := ""
	if !q.Since.IsZero() {
		since = rfc3339(q.Since)
	}
	where := []string{"1=1"}
	args := []any{since, since}
	switch q.Show {
	case "added", "removed":
		where = append(where, "action=?")
		args = append(args, q.Show)
	case "failed":
		where = append(where, "action='removed' AND status IN ('"+strings.Join(ProblemStatuses, "','")+"')")
	}
	if search := strings.TrimSpace(q.Search); search != "" {
		where = append(where, `title LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscaper.Replace(search)+"%")
	}
	from := `FROM (` + activityRows + `) WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := s.q.QueryRow(`SELECT COUNT(*) `+from, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.Query(`SELECT action,at,day,service_id,service_name,kind,source_id,title,year,files,bytes,COALESCE(episodes,''),origin,status,error,operation `+from+
		` ORDER BY at DESC LIMIT ? OFFSET ?`, append(args, limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		var at, episodes string
		if err := rows.Scan(&a.Action, &at, &a.Day, &a.ServiceID, &a.ServiceName, &a.Kind, &a.SourceID, &a.Title, &a.Year, &a.Files, &a.Bytes,
			&episodes, &a.Origin, &a.Status, &a.Error, &a.OperationID); err != nil {
			return nil, 0, err
		}
		a.At, _ = time.Parse(time.RFC3339Nano, at)
		a.Episodes = parseEpisodes(episodes)
		out = append(out, a)
	}
	return out, total, rows.Err()
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// parseEpisodes reads "season:episode,…", leaving out a film's "0:0".
func parseEpisodes(list string) [][2]int {
	var out [][2]int
	for _, item := range strings.Split(list, ",") {
		season, episode, ok := strings.Cut(item, ":")
		if !ok {
			continue
		}
		sn, _ := strconv.Atoi(season)
		en, _ := strconv.Atoi(episode)
		if en > 0 {
			out = append(out, [2]int{sn, en})
		}
	}
	return out
}

// ImportedFilesOn returns one title's files imported that day, by season
// and episode.
func (s *Store) ImportedFilesOn(serviceID, kind string, sourceID int, day string) ([]ImportedFile, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	rows, err := s.q.Query(`SELECT record_id,service_name,title,year,season,episode,path,size,origin,source_title,imported_at FROM imported_files
		WHERE service_id=? AND kind=? AND source_id=? AND day=? ORDER BY season, episode, imported_at`, serviceID, kind, sourceID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportedFile
	for rows.Next() {
		f := ImportedFile{ServiceID: serviceID, Kind: kind, SourceID: sourceID}
		var at string
		if err := rows.Scan(&f.RecordID, &f.ServiceName, &f.Title, &f.Year, &f.Season, &f.Episode, &f.Path, &f.Size, &f.Origin, &f.SourceTitle, &at); err != nil {
			return nil, err
		}
		f.ImportedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, f)
	}
	return out, rows.Err()
}
