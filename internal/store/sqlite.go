package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite" // the SQLite driver, in pure Go
)

// querier is what the store's statements run on: the database, or the
// transaction withWriteTx is in.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Prepare(query string) (*sql.Stmt, error)
}

type Store struct {
	db *sql.DB
	// q is db, or during withWriteTx its transaction: it changes only while
	// accessMu is held exclusively, so readers always see db.
	q            querier
	accessMu     sync.RWMutex
	beforeCommit func() error // test failure/blocking hook; nil in production
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	// One connection, as before: writes are serialized by accessMu, and a
	// write transaction must be the only thing on the database.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	s := &Store{db: db, q: db}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS media (kind TEXT NOT NULL, source_id INTEGER NOT NULL, service_id TEXT NOT NULL DEFAULT '', payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(kind,source_id,service_id));`,
		`CREATE TABLE IF NOT EXISTS torrents (client TEXT NOT NULL, hash TEXT NOT NULL, payload BLOB NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(client,hash));`,
		`CREATE TABLE IF NOT EXISTS unmanaged_files (path TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS files (path TEXT PRIMARY KEY, payload BLOB NOT NULL, updated_at TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS media_files (kind TEXT NOT NULL, source_id INTEGER NOT NULL, source TEXT NOT NULL, source_file_id INTEGER NOT NULL, path TEXT NOT NULL, service_id TEXT NOT NULL DEFAULT '', service_name TEXT NOT NULL DEFAULT '', PRIMARY KEY(kind,source_id,source,source_file_id));`,
		`CREATE INDEX IF NOT EXISTS idx_media_files_media ON media_files(kind,source_id);`,
		`CREATE INDEX IF NOT EXISTS idx_media_files_path ON media_files(path);`,
		`CREATE TABLE IF NOT EXISTS media_file_parts (kind TEXT NOT NULL, source_id INTEGER NOT NULL, source TEXT NOT NULL, source_file_id INTEGER NOT NULL, part_group TEXT NOT NULL DEFAULT '', part_label TEXT NOT NULL DEFAULT '', part_order INTEGER NOT NULL DEFAULT 0, source_part_id INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(kind,source_id,source,source_file_id,source_part_id));`,
		`CREATE INDEX IF NOT EXISTS idx_media_file_parts_file ON media_file_parts(kind,source_id,source,source_file_id);`,
		`CREATE TABLE IF NOT EXISTS torrent_files (client TEXT NOT NULL, hash TEXT NOT NULL, file_index INTEGER NOT NULL, path TEXT NOT NULL, service_id TEXT NOT NULL DEFAULT '', service_name TEXT NOT NULL DEFAULT '', PRIMARY KEY(client,hash,file_index));`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_files_hash ON torrent_files(client,hash);`,
		`CREATE INDEX IF NOT EXISTS idx_torrent_files_path ON torrent_files(path);`,
		`CREATE TABLE IF NOT EXISTS arr_imports (source TEXT NOT NULL, owner_id INTEGER NOT NULL, sub_id INTEGER NOT NULL DEFAULT 0, download_id TEXT NOT NULL, imported_at TEXT NOT NULL, service_id TEXT NOT NULL DEFAULT '', PRIMARY KEY(source,service_id,owner_id,sub_id,download_id,imported_at));`,
		`CREATE INDEX IF NOT EXISTS idx_arr_imports_source_owner ON arr_imports(source,service_id,owner_id,sub_id,imported_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_arr_imports_hash ON arr_imports(download_id);`,
		`CREATE TABLE IF NOT EXISTS history_events (id INTEGER PRIMARY KEY AUTOINCREMENT, event_type TEXT NOT NULL, status TEXT NOT NULL, dry_run INTEGER NOT NULL DEFAULT 1, requested_kind TEXT NOT NULL DEFAULT '', requested_key TEXT NOT NULL DEFAULT '', requested_label TEXT NOT NULL DEFAULT '', reclaimable_bytes INTEGER NOT NULL DEFAULT 0, payload BLOB NOT NULL DEFAULT '{}', error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_history_events_created ON history_events(created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS cleanup_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, started_at TEXT NOT NULL, completed_at TEXT NOT NULL, usage_before REAL NOT NULL, usage_after REAL NOT NULL, target_usage REAL NOT NULL, critical_usage REAL NOT NULL, planned_bytes INTEGER NOT NULL DEFAULT 0, media_bytes INTEGER NOT NULL DEFAULT 0, reclaimed_bytes INTEGER NOT NULL DEFAULT 0, media_removed INTEGER NOT NULL DEFAULT 0, torrents_removed INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '');`,
		`CREATE TABLE IF NOT EXISTS cleanup_actions (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id INTEGER NOT NULL, kind TEXT NOT NULL, source_id INTEGER NOT NULL DEFAULT 0, title TEXT NOT NULL DEFAULT '', value REAL NOT NULL DEFAULT 0, media_bytes INTEGER NOT NULL DEFAULT 0, reclaimed_bytes INTEGER NOT NULL DEFAULT 0, torrent_hash TEXT NOT NULL DEFAULT '', action TEXT NOT NULL, FOREIGN KEY(run_id) REFERENCES cleanup_runs(id));`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_runs_completed ON cleanup_runs(completed_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_cleanup_actions_run ON cleanup_actions(run_id);`,
		`CREATE TABLE IF NOT EXISTS sessions (token TEXT PRIMARY KEY, created_at TEXT NOT NULL, expires_at TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);`,
		`DELETE FROM sessions WHERE token NOT LIKE 'sha256:%';`,
		// Torrents' history, sampled every 30 minutes while Overtide scored
		// torrents on it: nothing reads it any more.
		`DROP TABLE IF EXISTS torrent_history;`,
		// IMDb's ratings dataset, from before Overtide stopped valuing titles
		// itself: over a million rows nothing reads any more.
		`DROP TABLE IF EXISTS imdb_ratings;`,
	} {
		if err := s.exec(q); err != nil {
			s.Close()
			return nil, err
		}
	}
	for _, migration := range []struct{ table, column, definition string }{
		{"media_files", "service_id", "TEXT NOT NULL DEFAULT ''"},
		{"media_files", "service_name", "TEXT NOT NULL DEFAULT ''"},
		{"torrent_files", "service_id", "TEXT NOT NULL DEFAULT ''"},
		{"torrent_files", "service_name", "TEXT NOT NULL DEFAULT ''"},
		{"history_events", "media_bytes", "INTEGER NOT NULL DEFAULT 0"},
		{"history_events", "service_name", "TEXT NOT NULL DEFAULT ''"},
		{"arr_imports", "service_id", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.ensureColumn(migration.table, migration.column, migration.definition); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) ensureColumn(table, column, definition string) error {
	rows, err := s.q.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if found {
		return nil
	}
	return s.exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db, s.q = nil, nil
	return err
}

func (s *Store) exec(query string, args ...any) error {
	if _, err := s.q.Exec(query, args...); err != nil {
		return fmt.Errorf("sqlite: %w", err)
	}
	return nil
}

// withWriteTx publishes a logical snapshot atomically. The caller must own
// accessMu exclusively so no reader can observe the DELETE/INSERT
// replacement while it is in progress.
func (s *Store) withWriteTx(fn func() error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	s.q = tx
	defer func() { s.q = s.db }()
	if err := fn(); err != nil {
		_ = tx.Rollback()
		return err
	}
	if s.beforeCommit != nil {
		if err := s.beforeCommit(); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// insertAll runs one prepared statement once per row of arguments.
func (s *Store) insertAll(query string, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	st, err := s.q.Prepare(query)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, args := range rows {
		if _, err := st.Exec(args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SetMeta(key, value string) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.setMeta(key, value)
}

// setMeta writes metadata while the caller already owns accessMu.
func (s *Store) setMeta(key, value string) error {
	return s.exec(`INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
}

func (s *Store) Meta(key string) (string, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	return s.meta(key)
}

func (s *Store) meta(key string) (string, error) {
	var v string
	err := s.q.QueryRow(`SELECT value FROM metadata WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) MetaInt64(key string) (int64, error) {
	v, e := s.Meta(key)
	if e != nil || v == "" {
		return 0, e
	}
	return strconv.ParseInt(v, 10, 64)
}

func (s *Store) SetMetaInt64(key string, v int64) error {
	return s.SetMeta(key, strconv.FormatInt(v, 10))
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// payloads reads one text column of every row, for JSON payloads.
func (s *Store) payloads(query string, args ...any) ([]string, error) {
	rows, err := s.q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// rfc3339 is how the store writes times.
func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
