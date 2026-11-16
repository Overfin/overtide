package httpui

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/overfin/overtide/internal/model"
)

// removalTorrentKey names a torrent in a removal: its client and its hash.
// The same hash can be in two qBittorrent clients (a cross-seed, a seedbox
// mirrored at home), and a removal takes only the one it was shown: the
// window's torrent fields, the plan's owner keys and the queued forms all
// carry this key.
func removalTorrentKey(serviceID, hash string) string {
	return strings.ToLower(strings.TrimSpace(serviceID)) + "|" + strings.ToLower(strings.TrimSpace(hash))
}

func torrentRemovalKey(t model.Torrent) string { return removalTorrentKey(t.ServiceID, t.Hash) }

// splitRemovalTorrentKey is a key's client and hash. A bare hash, as the
// removals queued before keys had clients carry, has no client.
func splitRemovalTorrentKey(value string) (serviceID, hash string) {
	value = strings.ToLower(strings.TrimSpace(value))
	if i := strings.LastIndex(value, "|"); i >= 0 {
		return value[:i], value[i+1:]
	}
	return "", value
}

// torrentKeys finds the torrents a removal names, from the torrents the
// last refresh saw.
type torrentKeys struct {
	byKey  map[string]model.Torrent
	byHash map[string][]model.Torrent
}

func newTorrentKeys(torrents []model.Torrent) torrentKeys {
	keys := torrentKeys{byKey: map[string]model.Torrent{}, byHash: map[string][]model.Torrent{}}
	for _, t := range torrents {
		keys.byKey[torrentRemovalKey(t)] = t
		hash := strings.ToLower(t.Hash)
		keys.byHash[hash] = append(keys.byHash[hash], t)
	}
	return keys
}

// resolve is the key a removal's torrent value stands for. A client|hash
// value is taken as it is (whether that torrent is still there is checked
// where it's used). A bare hash names its torrent only when a single client
// has it: in two, the removal is refused, never guessed.
func (keys torrentKeys) resolve(value string) (string, error) {
	serviceID, hash := splitRemovalTorrentKey(value)
	if hash == "" {
		return "", fmt.Errorf("empty torrent")
	}
	if serviceID != "" {
		return removalTorrentKey(serviceID, hash), nil
	}
	switch found := keys.byHash[hash]; len(found) {
	case 0:
		return "", fmt.Errorf("torrent not found: %s", hash)
	case 1:
		return torrentRemovalKey(found[0]), nil
	default:
		return "", fmt.Errorf("torrent %s is in more than one torrent client, and this removal doesn't say which: open its removal again", hash)
	}
}

// resolveAll is resolve for each value, as a set.
func (keys torrentKeys) resolveAll(values []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		key, err := keys.resolve(value)
		if err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, nil
}

// keyTorrentFields rewrites a removal form's torrent fields as client|hash
// keys (resolve), so what's queued and run names each torrent's client.
func keyTorrentFields(form url.Values, torrents []model.Torrent) error {
	values := form["torrent"]
	if len(values) == 0 {
		return nil
	}
	keys := newTorrentKeys(torrents)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		key, err := keys.resolve(value)
		if err != nil {
			return err
		}
		out = append(out, key)
	}
	form["torrent"] = out
	return nil
}

// torrentsKnown checks that every torrent a removal names is one the last
// refresh saw, in the client named.
func torrentsKnown(values []string, torrents []model.Torrent) error {
	keys := newTorrentKeys(torrents)
	for _, value := range values {
		key, err := keys.resolve(value)
		if err != nil {
			return err
		}
		if _, ok := keys.byKey[key]; !ok {
			return fmt.Errorf("torrent not found: %s", key)
		}
	}
	return nil
}
