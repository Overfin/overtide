package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/model"
)

func (s *Store) SaveUnmanagedFiles(items []model.UnmanagedFile) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.saveUnmanagedFiles(items) })
}

func (s *Store) saveUnmanagedFiles(items []model.UnmanagedFile) error {
	if err := s.exec("DELETE FROM unmanaged_files"); err != nil {
		return err
	}
	now := rfc3339(time.Now())
	rows, err := unmanagedRows(items, now)
	if err != nil {
		return err
	}
	if err := s.insertAll(`INSERT INTO unmanaged_files(path,payload,updated_at) VALUES(?,?,?)`, rows); err != nil {
		return err
	}
	return s.setMeta("unmanaged.updated_at", now)
}

func unmanagedRows(items []model.UnmanagedFile, now string) ([][]any, error) {
	rows := make([][]any, 0, len(items))
	for _, f := range items {
		b, err := json.Marshal(f)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []any{filepath.Clean(f.Path), string(b), now})
	}
	return rows, nil
}

func (s *Store) LoadUnmanagedFiles() ([]model.UnmanagedFile, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	payloads, err := s.payloads(`SELECT payload FROM unmanaged_files`)
	if err != nil {
		return nil, time.Time{}, err
	}
	var out []model.UnmanagedFile
	for _, p := range payloads {
		var f model.UnmanagedFile
		if err := json.Unmarshal([]byte(p), &f); err != nil {
			return nil, time.Time{}, err
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	ts, _ := s.meta("unmanaged.updated_at")
	t, _ := time.Parse(time.RFC3339Nano, ts)
	return out, t, nil
}

func (s *Store) ReplaceFiles(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error { return s.replaceFiles(files, mediaRefs, torrentRefs) })
}

const (
	insertFile         = `INSERT INTO files(path,payload,updated_at) VALUES(?,?,?)`
	insertMediaFile    = `INSERT INTO media_files(kind,source_id,source,source_file_id,path,service_id,service_name) VALUES(?,?,?,?,?,?,?)`
	insertMediaPart    = `INSERT INTO media_file_parts(kind,source_id,source,source_file_id,part_group,part_label,part_order,source_part_id) VALUES(?,?,?,?,?,?,?,?)`
	insertTorrentFile  = `INSERT INTO torrent_files(client,hash,file_index,path,service_id,service_name) VALUES(?,?,?,?,?,?)`
	insertUnmanagedRow = `INSERT INTO unmanaged_files(path,payload,updated_at) VALUES(?,?,?)`
)

func fileRows(files []model.File, now string) ([][]any, error) {
	rows := make([][]any, 0, len(files))
	for _, f := range files {
		b, err := json.Marshal(f)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []any{filepath.Clean(f.Path), string(b), now})
	}
	return rows, nil
}

// mediaRefRows are media_files rows and media_file_parts rows.
func mediaRefRows(refs []model.MediaFileRef) (files, parts [][]any) {
	for _, r := range refs {
		files = append(files, []any{string(r.MediaType), r.MediaID, r.Source, r.SourceFileID, filepath.Clean(r.Path), r.ServiceID, r.ServiceName})
		for _, part := range r.Parts {
			parts = append(parts, []any{string(r.MediaType), r.MediaID, r.Source, r.SourceFileID, part.Group, part.Label, part.Order, part.SourcePartID})
		}
	}
	return files, parts
}

func torrentRefRows(refs []model.TorrentFileRef) [][]any {
	rows := make([][]any, 0, len(refs))
	for _, r := range refs {
		rows = append(rows, []any{r.Client, strings.ToLower(r.Hash), r.FileIndex, filepath.Clean(r.Path), r.ServiceID, r.ServiceName})
	}
	return rows
}

func (s *Store) replaceFiles(files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef) error {
	for _, q := range []string{"DELETE FROM files", "DELETE FROM media_file_parts", "DELETE FROM media_files", "DELETE FROM torrent_files"} {
		if err := s.exec(q); err != nil {
			return err
		}
	}
	now := rfc3339(time.Now())
	rows, err := fileRows(files, now)
	if err != nil {
		return err
	}
	if err := s.insertAll(insertFile, rows); err != nil {
		return err
	}
	mediaFiles, parts := mediaRefRows(mediaRefs)
	if err := s.insertAll(insertMediaFile, mediaFiles); err != nil {
		return err
	}
	if err := s.insertAll(insertMediaPart, parts); err != nil {
		return err
	}
	if err := s.insertAll(insertTorrentFile, torrentRefRows(torrentRefs)); err != nil {
		return err
	}
	return s.setMeta("files.updated_at", now)
}

// PublishReconciliation commits every projection derived by one filesystem
// generation together. A crash or error therefore leaves the previous complete
// generation intact instead of mixing new topology with old Unmanaged, Media,
// or Torrent rows.
func (s *Store) PublishReconciliation(generation uint64, files []model.File, mediaRefs []model.MediaFileRef, torrentRefs []model.TorrentFileRef, unmanaged []model.UnmanagedFile, torrents []model.Torrent, media []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		if err := s.replaceFiles(files, mediaRefs, torrentRefs); err != nil {
			return err
		}
		if err := s.saveUnmanagedFiles(unmanaged); err != nil {
			return err
		}
		if err := s.saveTorrents(torrents); err != nil {
			return err
		}
		if err := s.saveMedia(media); err != nil {
			return err
		}
		return s.setMeta("generation.files", strconv.FormatUint(generation, 10))
	})
}

type MediaIdentity struct {
	Kind     model.MediaType
	SourceID int
}

type ReconciliationDelta struct {
	Paths []string
	Files []model.File
	// MediaOwners are fully re-published: every existing row for that owner is
	// deleted before MediaRefs is inserted. Omit an owner here (rather than
	// including it with no matching MediaRefs) to leave its rows untouched.
	MediaOwners []MediaIdentity
	MediaRefs   []model.MediaFileRef
	// RemovedTorrentHashes are fully re-published the same way: every existing
	// torrent_files row for that hash is deleted, then TorrentRefs is
	// inserted. A hash present here with no matching TorrentRefs is removed
	// outright rather than replaced.
	RemovedTorrentHashes []string
	TorrentRefs          []model.TorrentFileRef
	Unmanaged            []model.UnmanagedFile
	Torrents             []model.Torrent
	Media                []model.Media
	Generation           uint64
	ScopeMetadataKey     string
}

// PublishReconciliationDelta commits only topology rows affected by a known
// mutation. Global Media/Torrent projections are still published atomically
// because their values and relationship explanations may depend on those rows.
func (s *Store) PublishReconciliationDelta(delta ReconciliationDelta) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		now := rfc3339(time.Now())
		var paths [][]any
		for _, path := range delta.Paths {
			paths = append(paths, []any{filepath.Clean(path)})
		}
		if err := s.insertAll(`DELETE FROM files WHERE path=?`, paths); err != nil {
			return err
		}
		if err := s.insertAll(`DELETE FROM unmanaged_files WHERE path=?`, paths); err != nil {
			return err
		}
		rows, err := fileRows(delta.Files, now)
		if err != nil {
			return err
		}
		if err := s.insertAll(insertFile, rows); err != nil {
			return err
		}
		if rows, err = unmanagedRows(delta.Unmanaged, now); err != nil {
			return err
		}
		if err := s.insertAll(insertUnmanagedRow, rows); err != nil {
			return err
		}
		var owners [][]any
		for _, owner := range delta.MediaOwners {
			owners = append(owners, []any{string(owner.Kind), owner.SourceID})
		}
		if err := s.insertAll(`DELETE FROM media_file_parts WHERE kind=? AND source_id=?`, owners); err != nil {
			return err
		}
		if err := s.insertAll(`DELETE FROM media_files WHERE kind=? AND source_id=?`, owners); err != nil {
			return err
		}
		mediaFiles, parts := mediaRefRows(delta.MediaRefs)
		if err := s.insertAll(insertMediaFile, mediaFiles); err != nil {
			return err
		}
		if err := s.insertAll(insertMediaPart, parts); err != nil {
			return err
		}
		var hashes [][]any
		for _, hash := range delta.RemovedTorrentHashes {
			hashes = append(hashes, []any{strings.ToLower(strings.TrimSpace(hash))})
		}
		if err := s.insertAll(`DELETE FROM torrent_files WHERE hash=?`, hashes); err != nil {
			return err
		}
		if err := s.insertAll(insertTorrentFile, torrentRefRows(delta.TorrentRefs)); err != nil {
			return err
		}
		if err := s.saveTorrents(delta.Torrents); err != nil {
			return err
		}
		if err := s.saveMedia(delta.Media); err != nil {
			return err
		}
		if err := s.setMeta("files.updated_at", now); err != nil {
			return err
		}
		if err := s.setMeta("unmanaged.updated_at", now); err != nil {
			return err
		}
		if err := s.setMeta("generation.files", strconv.FormatUint(delta.Generation, 10)); err != nil {
			return err
		}
		if delta.ScopeMetadataKey != "" {
			if err := s.setMeta(delta.ScopeMetadataKey, ""); err != nil {
				return err
			}
		}
		return nil
	})
}

// PublishInventory atomically advances every configured qBittorrent
// instance's incremental cursor (keyed by service ID, since each
// instance has its own independent sync stream) with the Media and Torrent
// snapshot to which they belong. Replaying an old RID is safe; advancing it
// without its snapshot is not.
func (s *Store) PublishInventory(generation uint64, torrentRIDs map[string]int64, torrents []model.Torrent, media []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		if err := s.saveTorrents(torrents); err != nil {
			return err
		}
		for serviceID, rid := range torrentRIDs {
			if rid == 0 {
				continue
			}
			if err := s.setMeta("qbittorrent."+serviceID+".rid", strconv.FormatInt(rid, 10)); err != nil {
				return err
			}
		}
		if err := s.saveMedia(media); err != nil {
			return err
		}
		return s.setMeta("generation.inventory", strconv.FormatUint(generation, 10))
	})
}

// PublishEnrichment replaces only Media valuation inputs/results and records
// the base generation they enrich. It cannot disturb Torrent or File topology.
func (s *Store) PublishEnrichment(name string, generation uint64, media []model.Media) error {
	s.accessMu.Lock()
	defer s.accessMu.Unlock()
	return s.withWriteTx(func() error {
		if err := s.saveMedia(media); err != nil {
			return err
		}
		return s.setMeta("generation.enrichment."+name, strconv.FormatUint(generation, 10))
	})
}

func (s *Store) LoadFiles() ([]model.File, []model.MediaFileRef, []model.TorrentFileRef, time.Time, error) {
	s.accessMu.RLock()
	defer s.accessMu.RUnlock()
	payloads, err := s.payloads(`SELECT payload FROM files ORDER BY path`)
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	var files []model.File
	for _, p := range payloads {
		var f model.File
		if err := json.Unmarshal([]byte(p), &f); err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		files = append(files, f)
	}
	mr, err := s.mediaFileRefs()
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	tr, err := s.torrentFileRefs()
	if err != nil {
		return nil, nil, nil, time.Time{}, err
	}
	ts, _ := s.meta("files.updated_at")
	updated, _ := time.Parse(time.RFC3339Nano, ts)
	return files, mr, tr, updated, nil
}

func (s *Store) mediaFileRefs() ([]model.MediaFileRef, error) {
	rows, err := s.q.Query(`SELECT kind,source_id,source,source_file_id,path,service_id,service_name FROM media_files ORDER BY kind,source_id,path`)
	if err != nil {
		return nil, err
	}
	var mr []model.MediaFileRef
	for rows.Next() {
		var r model.MediaFileRef
		var kind string
		if err := rows.Scan(&kind, &r.MediaID, &r.Source, &r.SourceFileID, &r.Path, &r.ServiceID, &r.ServiceName); err != nil {
			rows.Close()
			return nil, err
		}
		r.MediaType = model.MediaType(kind)
		mr = append(mr, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	partsByFile := map[string][]model.MediaFilePart{}
	rows, err = s.q.Query(`SELECT kind,source_id,source,source_file_id,part_group,part_label,part_order,source_part_id FROM media_file_parts ORDER BY kind,source_id,source_file_id,part_order,part_label`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var kind, source string
		var sourceID, sourceFileID int
		var part model.MediaFilePart
		if err := rows.Scan(&kind, &sourceID, &source, &sourceFileID, &part.Group, &part.Label, &part.Order, &part.SourcePartID); err != nil {
			rows.Close()
			return nil, err
		}
		key := fmt.Sprintf("%s:%d:%s:%d", kind, sourceID, source, sourceFileID)
		partsByFile[key] = append(partsByFile[key], part)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range mr {
		key := fmt.Sprintf("%s:%d:%s:%d", mr[i].MediaType, mr[i].MediaID, mr[i].Source, mr[i].SourceFileID)
		mr[i].Parts = append([]model.MediaFilePart(nil), partsByFile[key]...)
	}
	return mr, nil
}

func (s *Store) torrentFileRefs() ([]model.TorrentFileRef, error) {
	rows, err := s.q.Query(`SELECT client,hash,file_index,path,service_id,service_name FROM torrent_files ORDER BY client,hash,file_index`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tr []model.TorrentFileRef
	for rows.Next() {
		var r model.TorrentFileRef
		if err := rows.Scan(&r.Client, &r.Hash, &r.FileIndex, &r.Path, &r.ServiceID, &r.ServiceName); err != nil {
			return nil, err
		}
		tr = append(tr, r)
	}
	return tr, rows.Err()
}
