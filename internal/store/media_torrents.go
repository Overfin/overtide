package store

import (
	"encoding/json"
	"github.com/overfin/overtide/internal/model"
	"sort"
	"strings"
	"time"
)

func (s *Store) SaveMedia(items []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.saveMedia(items) })
}

func (s *Store) saveMedia(items []model.Media) error {
	if err := s.exec("DELETE FROM media"); err != nil {
		return err
	}
	now := rfc3339(time.Now())
	rows := make([][]any, 0, len(items))
	for _, m := range items {
		b, e := json.Marshal(m)
		if e != nil {
			return e
		}
		rows = append(rows, []any{string(m.Type), m.SourceID, m.ServiceID, string(b), now})
	}
	if err := s.insertAll(`INSERT INTO media(kind,source_id,service_id,payload,updated_at) VALUES(?,?,?,?,?)`, rows); err != nil {
		return err
	}
	return s.setMeta("media.updated_at", now)
}

func (s *Store) LoadMedia() ([]model.Media, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	payloads, e := s.payloads(`SELECT payload FROM media`)
	if e != nil {
		return nil, time.Time{}, e
	}
	var out []model.Media
	for _, p := range payloads {
		var m model.Media
		if e := json.Unmarshal([]byte(p), &m); e != nil {
			return nil, time.Time{}, e
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RetentionValue != out[j].RetentionValue {
			return out[i].RetentionValue < out[j].RetentionValue
		}
		return out[i].SizeBytes > out[j].SizeBytes
	})
	ts, _ := s.meta("media.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}

func (s *Store) SaveTorrents(items []model.Torrent) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.saveTorrents(items) })
}

func (s *Store) saveTorrents(items []model.Torrent) error {
	if err := s.exec("DELETE FROM torrents"); err != nil {
		return err
	}
	now := rfc3339(time.Now())
	rows := make([][]any, 0, len(items))
	for _, p := range items {
		// Persist only the torrent index Overtide needs for lists, valuation,
		// relationships and storage work. Client-owned diagnostics are lazy.
		// Private, SeedingTime and AddedOn stay: protections depend on them, and
		// qBittorrent's incremental sync only resends what changes.
		p.Tracker = ""
		p.TotalSizeBytes = 0
		p.CompletedBytes = 0
		p.AmountLeftBytes = 0
		p.DownloadedBytes = 0
		p.UploadedBytes = 0
		p.DownloadedSession = 0
		p.UploadedSession = 0
		p.DownloadLimit = 0
		p.UploadLimit = 0
		p.MaxRatio = 0
		p.Availability = 0
		p.CompletionOn = 0
		p.SeenComplete = 0
		p.TimeActive = 0
		p.ETA = 0
		p.Reannounce = 0
		p.ForceStart = false
		p.AutoTMM = false
		p.Sequential = false
		p.SuperSeeding = false
		b, e := json.Marshal(p)
		if e != nil {
			return e
		}
		rows = append(rows, []any{p.Client, strings.ToLower(p.Hash), string(b), now})
	}
	if err := s.insertAll(`INSERT INTO torrents(client,hash,payload,updated_at) VALUES(?,?,?,?)`, rows); err != nil {
		return err
	}
	return s.setMeta("torrents.updated_at", now)
}

func (s *Store) LoadTorrents() ([]model.Torrent, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	payloads, e := s.payloads(`SELECT payload FROM torrents`)
	if e != nil {
		return nil, e
	}
	var out []model.Torrent
	for _, raw := range payloads {
		var p model.Torrent
		if e := json.Unmarshal([]byte(raw), &p); e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
