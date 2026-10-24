package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type HistoryEvent struct {
	ID               int64  `json:"id"`
	EventType        string `json:"eventType"`
	Status           string `json:"status"`
	DryRun           bool   `json:"dryRun"`
	RequestedKind    string `json:"requestedKind"`
	RequestedKey     string `json:"requestedKey"`
	RequestedLabel   string `json:"requestedLabel"`
	ReclaimableBytes int64  `json:"reclaimableBytes"`
	// MediaBytes is the raw sum of every selected file's own size, before
	// hardlink deduplication — "Library bytes removed". It is deliberately
	// tracked separately from ReclaimableBytes ("actual filesystem bytes
	// reclaimed"), since hardlinks mean those are not necessarily equal.
	MediaBytes int64           `json:"mediaBytes"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      string          `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	// ServiceName is the owning service's configured name (empty for an
	// Unmanaged event, which has no single owner) — set once at creation
	// and never touched by UpdateHistoryEvent, so a past run's per-service
	// breakdown stays inspectable regardless of how its status changed
	// afterward.
	ServiceName string `json:"serviceName,omitempty"`
}

func (s *Store) SaveHistoryEvent(e HistoryEvent) (int64, error) {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	res, err := s.q.Exec(`INSERT INTO history_events(event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes,service_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.EventType, e.Status, boolInt(e.DryRun), e.RequestedKind, e.RequestedKey, e.RequestedLabel, e.ReclaimableBytes,
		string(e.Payload), e.Error, rfc3339(e.CreatedAt), e.MediaBytes, e.ServiceName)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateHistoryEvent finalizes the durable operation row created before a
// removal begins. The operation identity is preserved across every outcome.
func (s *Store) UpdateHistoryEvent(e HistoryEvent) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	if e.ID <= 0 {
		return fmt.Errorf("history event id is required")
	}
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	res, err := s.q.Exec(`UPDATE history_events SET status=?,dry_run=?,requested_kind=?,requested_key=?,requested_label=?,reclaimable_bytes=?,payload=?,error=?,media_bytes=? WHERE id=?`,
		e.Status, boolInt(e.DryRun), e.RequestedKind, e.RequestedKey, e.RequestedLabel, e.ReclaimableBytes,
		string(e.Payload), e.Error, e.MediaBytes, e.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("history event %d does not exist", e.ID)
	}
	return nil
}

// InterruptStartedHistoryEvents closes operations left in-flight by a prior
// process exit. Their exact starting plan remains available in payload.
func (s *Store) InterruptStartedHistoryEvents() error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.exec(`UPDATE history_events SET status='interrupted', error=CASE WHEN error='' THEN 'application stopped before the removal operation was finalized' ELSE error END WHERE event_type='removal' AND status='started'`)
}

const historyColumns = `id,event_type,status,dry_run,requested_kind,requested_key,requested_label,reclaimable_bytes,payload,error,created_at,media_bytes,service_name`

// scanHistoryEvent reads one row of historyColumns.
func scanHistoryEvent(row interface{ Scan(...any) error }) (HistoryEvent, error) {
	var e HistoryEvent
	var dryRun int64
	var payload, created string
	err := row.Scan(&e.ID, &e.EventType, &e.Status, &dryRun, &e.RequestedKind, &e.RequestedKey, &e.RequestedLabel,
		&e.ReclaimableBytes, &payload, &e.Error, &created, &e.MediaBytes, &e.ServiceName)
	if err != nil {
		return e, err
	}
	e.DryRun = dryRun != 0
	e.Payload = json.RawMessage(payload)
	e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return e, nil
}

func (s *Store) historyEvents(query string, args ...any) ([]HistoryEvent, error) {
	rows, err := s.q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEvent
	for rows.Next() {
		e, err := scanHistoryEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InFlightRemovalHistoryEvents returns the domain journal entries that need to
// be reconciled with scheduler state before new work starts after a restart.
func (s *Store) InFlightRemovalHistoryEvents() ([]HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	return s.historyEvents(`SELECT ` + historyColumns + ` FROM history_events WHERE event_type='removal' AND status IN ('queued','started') ORDER BY id`)
}

func (s *Store) HistoryEvents(limit int) ([]HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	return s.historyEvents(`SELECT `+historyColumns+` FROM history_events ORDER BY created_at DESC LIMIT ?`, limit)
}

func (s *Store) HistoryEventByID(id int64) (HistoryEvent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	if id <= 0 {
		return HistoryEvent{}, fmt.Errorf("history event id is required")
	}
	e, err := scanHistoryEvent(s.q.QueryRow(`SELECT `+historyColumns+` FROM history_events WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return HistoryEvent{}, fmt.Errorf("history event %d does not exist", id)
	}
	return e, err
}
