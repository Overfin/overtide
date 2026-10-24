package store

import (
	"strings"
	"time"
)

type ImportEvent struct {
	Source     string
	ServiceID  string
	OwnerID    int
	SubID      int
	DownloadID string
	ImportedAt time.Time
}

func (s *Store) AddImportEvents(events []ImportEvent) error {
	if len(events) == 0 {
		return nil
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	rows := make([][]any, 0, len(events))
	for _, x := range events {
		rows = append(rows, []any{x.Source, x.OwnerID, x.SubID, strings.ToLower(strings.TrimSpace(x.DownloadID)), rfc3339(x.ImportedAt), x.ServiceID})
	}
	return s.withWriteTx(func() error {
		return s.insertAll(`INSERT OR IGNORE INTO arr_imports(source,owner_id,sub_id,download_id,imported_at,service_id) VALUES(?,?,?,?,?,?)`, rows)
	})
}

// ImportEvents returns import history for one service instance of
// source ("radarr"/"sonarr"). serviceID disambiguates movie/series IDs
// across multiple configured instances of the same type; pass "" to match
// only pre-multi-instance rows persisted before this column existed.
func (s *Store) ImportEvents(source, serviceID string) ([]ImportEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	rows, e := s.q.Query(`SELECT owner_id,sub_id,download_id,imported_at FROM arr_imports WHERE source=? AND service_id=? ORDER BY imported_at DESC`, source, serviceID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []ImportEvent
	for rows.Next() {
		x := ImportEvent{Source: source, ServiceID: serviceID}
		var at string
		if e := rows.Scan(&x.OwnerID, &x.SubID, &x.DownloadID, &at); e != nil {
			return nil, e
		}
		x.ImportedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) AllImportHashes() (map[string]bool, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	hashes, e := s.payloads(`SELECT DISTINCT download_id FROM arr_imports`)
	if e != nil {
		return nil, e
	}
	out := map[string]bool{}
	for _, h := range hashes {
		out[strings.ToLower(h)] = true
	}
	return out, nil
}
