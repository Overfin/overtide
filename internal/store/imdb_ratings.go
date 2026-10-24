package store

import (
	"time"

	"github.com/overfin/overtide/internal/services/imdb"
)

// ReplaceIMDbRatings atomically swaps in a full new snapshot of IMDb's
// ratings dataset — mirrors saveMedia's exact shape (one write
// transaction: delete everything, then batch-insert) since this is the
// same kind of full-refresh bulk data, not incremental per-row updates.
func (s *Store) ReplaceIMDbRatings(ratings []imdb.Rating) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.replaceIMDbRatings(ratings) })
}

func (s *Store) replaceIMDbRatings(ratings []imdb.Rating) error {
	if err := s.exec("DELETE FROM imdb_ratings"); err != nil {
		return err
	}
	rows := make([][]any, 0, len(ratings))
	for _, r := range ratings {
		rows = append(rows, []any{r.Tconst, r.Value, r.Votes})
	}
	if err := s.insertAll(`INSERT INTO imdb_ratings(tconst,rating,votes) VALUES(?,?,?)`, rows); err != nil {
		return err
	}
	return s.setMeta("imdb_ratings.updated_at", rfc3339(time.Now()))
}

// LoadIMDbRatings returns the full current snapshot, keyed by IMDb id
// (tconst), along with when it was last successfully replaced — loaded
// once at startup and cached in memory by the caller rather than
// re-queried on every valuation pass (this table can hold well over a
// million rows).
func (s *Store) LoadIMDbRatings() (map[string]imdb.Rating, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	rows, e := s.q.Query(`SELECT tconst,rating,votes FROM imdb_ratings`)
	if e != nil {
		return nil, time.Time{}, e
	}
	out := map[string]imdb.Rating{}
	for rows.Next() {
		var r imdb.Rating
		if e := rows.Scan(&r.Tconst, &r.Value, &r.Votes); e != nil {
			rows.Close()
			return nil, time.Time{}, e
		}
		out[r.Tconst] = r
	}
	rows.Close()
	if e := rows.Err(); e != nil {
		return nil, time.Time{}, e
	}
	ts, _ := s.meta("imdb_ratings.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}
