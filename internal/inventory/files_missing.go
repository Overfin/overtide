package inventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/overfin/overtide/internal/model"
)

// MissingTorrentResult is what ForgetMissingTorrents did with one torrent.
type MissingTorrentResult struct {
	Torrent model.Torrent
	// Removed is true when qBittorrent dropped the torrent (or would have,
	// in dry run). Otherwise Reason says why it was left alone.
	Removed bool
	Reason  string
}

// ForgetMissingTorrents removes from qBittorrent the torrents whose files
// are gone from disk, leaving any file in place: it only tidies the client.
// Everything is checked again now, not taken from the last reconciliation:
// each of the torrent's files must be absent, and the storage root holding
// it must have something in it (an empty root is more likely a share or a
// disk that isn't mounted than files that are gone). A torrent kept by the
// operator, with an obligation, or whose service doesn't allow removal is
// left alone. In dry run, nothing is sent to qBittorrent.
func (service *Service) ForgetMissingTorrents(ctx context.Context) []MissingTorrentResult {
	service.mu.RLock()
	dryRun := service.cfg.Removal.DryRun
	roots := append([]storageRoot(nil), service.storageRoots...)
	refs := append([]model.TorrentFileRef(nil), service.torrentFileRefs...)
	var torrents []model.Torrent
	for _, t := range service.torrents {
		if t.FilesMissing {
			torrents = append(torrents, t)
		}
	}
	service.mu.RUnlock()

	pathsByTorrent := map[string][]string{}
	for _, ref := range refs {
		key := ref.ServiceID + "|" + strings.ToLower(ref.Hash)
		pathsByTorrent[key] = append(pathsByTorrent[key], ref.Path)
	}
	var results []MissingTorrentResult
	for _, t := range torrents {
		result := MissingTorrentResult{Torrent: t}
		switch {
		case t.Obligation != "":
			result.Reason = t.Obligation
		case t.RemovalRestricted:
			result.Reason = "its service doesn't allow removal"
		default:
			result.Reason = filesStillMissing(pathsByTorrent[t.ServiceID+"|"+strings.ToLower(t.Hash)], roots)
		}
		if result.Reason == "" {
			if dryRun {
				result.Removed = true
			} else if client, err := service.qbittorrentClient(t.ServiceID); err != nil {
				result.Reason = err.Error()
			} else if err := client.WithContext(ctx).DeleteKeepFiles(t.Hash); err != nil {
				result.Reason = err.Error()
			} else {
				result.Removed = true
			}
		}
		results = append(results, result)
	}
	if len(results) > 0 && !dryRun {
		service.publishChange()
	}
	return results
}

// filesStillMissing returns "" when every path is absent from disk and
// lies under a storage root that has something in it, else why not.
func filesStillMissing(paths []string, roots []storageRoot) string {
	if len(paths) == 0 {
		return "its files are unknown"
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return "its files are back on disk"
		}
		root := rootOf(path, roots)
		if root == "" {
			return "it isn't under a known storage root"
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) == 0 {
			return fmt.Sprintf("%s is empty or unreadable: is it mounted?", root)
		}
	}
	return ""
}

// rootOf returns the storage root path holds, or "".
func rootOf(path string, roots []storageRoot) string {
	path = filepath.Clean(path)
	for _, root := range roots {
		r := filepath.Clean(root.Path)
		if path == r || strings.HasPrefix(path, r+string(filepath.Separator)) {
			return r
		}
	}
	return ""
}
