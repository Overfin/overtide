package cleanup

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/overfin/overtide/internal/model"
)

// ActionKind distinguishes the three shapes a cleanup candidate can take.
type ActionKind string

const (
	// StandaloneMedia is a Media item with no Current hardlinked torrent;
	// removing it alone frees its own bytes.
	StandaloneMedia ActionKind = "media"
	// StandaloneTorrent is a Torrent that is not part of any HardlinkedBundle
	// (Superseded, Orphaned, Unassociated, or a proven non-hardlinked
	// Current copy); removing it alone frees its own bytes.
	StandaloneTorrent ActionKind = "torrent"
	// HardlinkedBundle is a Media item (or one Season of it) plus every
	// Current torrent physically hardlinked to it. These share an inode and
	// must be removed together or not at all: removing only one frees none
	// of the shared bytes while still destroying real value.
	HardlinkedBundle ActionKind = "bundle"
	// StandaloneSeason is one Season of a Series with no Current hardlinked
	// torrent; removing it alone frees its own bytes. A Series with
	// populated Seasons is always ranked per season instead of as one
	// whole-series StandaloneMedia candidate.
	StandaloneSeason ActionKind = "season"
)

// Action is one indivisible cleanup candidate.
type Action struct {
	Kind ActionKind `json:"kind"`
	// Media is set for StandaloneMedia, StandaloneSeason, and
	// HardlinkedBundle; zero value for StandaloneTorrent.
	Media model.Media `json:"media,omitempty"`
	// Season is set for StandaloneSeason and for a HardlinkedBundle scoped to
	// one season of a Series; nil otherwise (a HardlinkedBundle for a Movie,
	// or a StandaloneMedia/StandaloneTorrent action).
	Season *model.Season `json:"season,omitempty"`
	// Torrents has length 1 for StandaloneTorrent, one or more for
	// HardlinkedBundle, and is nil for StandaloneMedia/StandaloneSeason.
	Torrents []model.Torrent `json:"torrents,omitempty"`
	// Value is a title's (or season's) Overfin score, which orders titles.
	Value float64 `json:"value"`
	// LastActivity is when a lone torrent (or the most recently active of
	// a cross-seed group) last sent or received data, which orders lone
	// torrents: the one inactive the longest goes first. Lone torrents
	// always come before titles.
	LastActivity     int64          `json:"lastActivity,omitempty"`
	ReclaimableBytes int64          `json:"reclaimableBytes"`
	Reasons          []model.Reason `json:"reasons,omitempty"`
	// Folder is the root folder of a title when the disk's space is shared
	// between its folders (see Folder).
	Folder string `json:"folder,omitempty"`
	// Together are the ActionKeys of the other titles or seasons a torrent
	// of this one also backs (a pack bundled with several): they go all
	// together or not at all, and the torrent with the first of them.
	Together []string `json:"together,omitempty"`
}

// torrentKey identifies a torrent: its client and its hash, since the same
// hash can be in two clients.
func torrentKey(t model.Torrent) string {
	return t.ServiceID + "|" + strings.ToLower(t.Hash)
}

// Folder is a root folder of a disk whose space is shared between its
// folders: what it holds and its share, in bytes.
type Folder struct {
	Path  string `json:"path"`
	Bytes uint64 `json:"bytes"`
	Share uint64 `json:"share"`
}

// ActionKey is a stable identifier for one Action, independent of its
// position in a Plan's Actions slice (which can shift between two Build
// calls, e.g. once an item is excluded and a replacement takes its
// place) — content-derived from exactly what already uniquely identifies
// a torrent or a media item/season everywhere else in this codebase.
// Used both to let a caller exclude a specific candidate from selection
// (see Build's excluded parameter) and to re-locate one exact action a
// UI is acting on (protecting, say) in a freshly rebuilt Plan.
func ActionKey(action Action) string {
	if action.Kind == StandaloneTorrent {
		if len(action.Torrents) == 0 {
			return ""
		}
		return "torrent:" + strings.ToLower(action.Torrents[0].Hash) + ":" + action.Torrents[0].ServiceID
	}
	season := ""
	if action.Season != nil {
		season = strconv.Itoa(action.Season.Number)
	}
	return "media:" + string(action.Media.Type) + ":" + action.Media.ServiceID + ":" + strconv.Itoa(action.Media.SourceID) + ":" + season
}

type Plan struct {
	// Ordered is every action possible, in order; Actions is as many of them
	// as reach the target.
	Ordered              []Action `json:"-"`
	TargetUsagePercent   float64  `json:"targetUsagePercent"`
	CriticalUsagePercent float64  `json:"criticalUsagePercent"`
	Reliable             bool     `json:"reliable"`
	Available            bool     `json:"available"`
	Path                 string   `json:"path"`
	TotalBytes           uint64   `json:"totalBytes"`
	UsedBytes            uint64   `json:"usedBytes"`
	FreeBytes            uint64   `json:"freeBytes"`
	// OtherBytes is the real disk usage this plan was told to set aside as
	// outside Overtide's own view (see inventory.StorageDevice.OtherBytes) —
	// carried through so a caller can see what UsableBytes/UsagePercent were
	// actually computed against.
	OtherBytes uint64 `json:"otherBytes"`
	// UsableBytes is TotalBytes minus OtherBytes: the capacity
	// TargetUsagePercent/CriticalUsagePercent are evaluated against, not the
	// raw disk total. See UsagePercent.
	UsableBytes uint64 `json:"usableBytes"`
	// OvertideUsedBytes is UsedBytes minus OtherBytes.
	OvertideUsedBytes uint64 `json:"overtideUsedBytes"`
	// UsagePercent is OvertideUsedBytes/UsableBytes, not UsedBytes/TotalBytes:
	// target/critical mean "percent of what Overtide actually has to work
	// with," not percent of the raw disk, so space held by other services on
	// a shared pool is never something Overtide's own thresholds silently
	// have to compete against.
	UsagePercent  float64  `json:"usagePercent"`
	NeedBytes     uint64   `json:"needBytes"`
	Actions       []Action `json:"actions"`
	SelectedBytes uint64   `json:"selectedBytes"`
	Message       string   `json:"message"`
	Error         string   `json:"error,omitempty"`
	// Folders, when the disk's space is shared between its root folders:
	// then titles go one at a time, from the folder furthest over its
	// share, until the disk is back at its target or no folder is over.
	Folders []Folder `json:"folders,omitempty"`
}

// torrentsHoldBack reports whether any of a title's hardlinked torrents
// keeps the title from being removed: its service hasn't allowed removal,
// or it has an obligation (kept, private seeding time, minimum ratio). A
// torrent merely too new to judge doesn't: torrents don't weigh on a
// title's value.
func torrentsHoldBack(torrents []model.Torrent) bool {
	for _, t := range torrents {
		if t.RemovalRestricted || t.Obligation != "" {
			return true
		}
	}
	return false
}

// classify splits media and torrents into removal-candidate Actions, one
// tier per domain, applying every granular eligibility rule (protection,
// hardlink safety, reclaimability) but not yet ordering either tier.
func classify(media []model.Media, torrents []model.Torrent) (mediaTier, torrentTier []Action) {
	// backers counts the titles and seasons each hardlinked torrent backs,
	// candidates or not.
	backers := map[string]int{}

	for _, m := range media {
		if m.Type == model.Series && len(m.Seasons) > 0 {
			for _, season := range m.Seasons {
				hardlinked := m.CurrentHardlinkedTorrentsForSeason(season.Number)
				if len(hardlinked) > 0 {
					for _, t := range hardlinked {
						backers[torrentKey(t)]++
					}
					if season.Protected || season.RemovalRestricted || torrentsHoldBack(hardlinked) || !season.BundleReclaimableKnown || season.BundleReclaimableBytes <= 0 {
						continue
					}
					mediaTier = append(mediaTier, Action{Kind: HardlinkedBundle, Media: m, Season: &season, Torrents: hardlinked, Value: season.RetentionValue, ReclaimableBytes: season.BundleReclaimableBytes, Reasons: season.RetentionValueReasons})
					continue
				}
				if season.Protected || season.RemovalRestricted || !season.ReclaimableKnown || season.ReclaimableBytes <= 0 {
					continue
				}
				mediaTier = append(mediaTier, Action{Kind: StandaloneSeason, Media: m, Season: &season, Value: season.RetentionValue, ReclaimableBytes: season.ReclaimableBytes, Reasons: season.RetentionValueReasons})
			}
			continue
		}
		hardlinked := m.CurrentHardlinkedTorrents()
		if len(hardlinked) > 0 {
			for _, t := range hardlinked {
				backers[torrentKey(t)]++
			}
			if m.Protected || m.RemovalRestricted || torrentsHoldBack(hardlinked) || !m.BundleReclaimableKnown || m.BundleReclaimableBytes <= 0 {
				// Never offer the media or any of its hardlinked torrents
				// alone: doing so would free ~0 bytes while still destroying
				// real value, which is worse than doing nothing. A torrent
				// with an obligation holds the whole bundle back for the same
				// reason: removing the bundle would still delete files that
				// torrent needs. A torrent or media item whose own service
				// hasn't allowed removal holds it back the same way.
				continue
			}
			mediaTier = append(mediaTier, Action{Kind: HardlinkedBundle, Media: m, Torrents: hardlinked, Value: m.RetentionValue, ReclaimableBytes: m.BundleReclaimableBytes, Reasons: m.RetentionValueReasons})
			continue
		}
		if m.Protected || m.RemovalRestricted || !m.ReclaimableKnown || m.ReclaimableBytes <= 0 {
			continue
		}
		mediaTier = append(mediaTier, Action{Kind: StandaloneMedia, Media: m, Value: m.RetentionValue, ReclaimableBytes: m.ReclaimableBytes, Reasons: m.RetentionValueReasons})
	}
	mediaTier = sharedPacks(mediaTier, backers)

	byHash := make(map[string]model.Torrent, len(torrents))
	for _, t := range torrents {
		byHash[torrentKey(t)] = t
	}
	seenGroups := map[string]bool{}
	for _, t := range torrents {
		if t.Protected || t.RemovalRestricted {
			continue
		}
		if model.NormalizeTorrentStatus(t.AssociationStatus) == model.TorrentCurrent {
			// It backs a title: it goes with the title's bundle above, or
			// not at all. Removed alone it might free nothing while
			// destroying real swarm value. Once its title is gone, it's a
			// lone torrent like any other.
			continue
		}
		if len(t.SharesFilesWith) > 0 {
			// A cross-seed group: removed together, or not at all.
			if group := shareGroup(t, byHash); group != nil && !seenGroups[torrentKey(group[0])] {
				seenGroups[torrentKey(group[0])] = true
				if action, ok := groupAction(group); ok {
					torrentTier = append(torrentTier, action)
				}
			}
			continue
		}
		if !t.ReclaimableKnown || t.ReclaimableBytes <= 0 {
			continue
		}
		torrentTier = append(torrentTier, Action{Kind: StandaloneTorrent, Torrents: []model.Torrent{t}, LastActivity: t.LastActivity, ReclaimableBytes: t.ReclaimableBytes})
	}

	return mediaTier, torrentTier
}

// shareGroup returns t and every torrent of its client it shares files
// with, ordered by hash, or nil if one of them is unknown.
func shareGroup(t model.Torrent, byHash map[string]model.Torrent) []model.Torrent {
	group := []model.Torrent{t}
	for _, hash := range t.SharesFilesWith {
		other, ok := byHash[t.ServiceID+"|"+strings.ToLower(hash)]
		if !ok {
			return nil
		}
		group = append(group, other)
	}
	sort.Slice(group, func(i, j int) bool { return strings.ToLower(group[i].Hash) < strings.ToLower(group[j].Hash) })
	return group
}

// groupAction makes one lone-torrent action of a cross-seed group: as
// recently active as its most recently active torrent, freeing what the
// group frees together. None of them may be protected or held back, and
// none may back a title.
func groupAction(group []model.Torrent) (Action, bool) {
	action := Action{Kind: StandaloneTorrent, Torrents: group, ReclaimableBytes: group[0].GroupReclaimableBytes}
	for _, t := range group {
		if t.Protected || t.RemovalRestricted || model.NormalizeTorrentStatus(t.AssociationStatus) == model.TorrentCurrent || !t.GroupReclaimableKnown {
			return Action{}, false
		}
		if t.LastActivity > action.LastActivity {
			action.LastActivity = t.LastActivity
		}
	}
	if action.ReclaimableBytes <= 0 {
		return Action{}, false
	}
	return action, true
}

// sharedPacks deals with torrents bundled with more than one title or
// season (a pack): removing one of them with the pack would take the pack
// the others still seed, and each one's bundle counts the pack's own files
// again. A pack also backing a title or season that isn't a candidate (kept,
// protected, held back) can't go: no bundle holding it is offered, nor any
// linked to those through another pack. The others are linked (Together):
// they go together, or not at all.
func sharedPacks(tier []Action, backers map[string]int) []Action {
	holders := map[string][]int{}
	for i, action := range tier {
		if action.Kind != HardlinkedBundle {
			continue
		}
		for _, t := range action.Torrents {
			holders[torrentKey(t)] = append(holders[torrentKey(t)], i)
		}
	}
	dropped := map[int]bool{}
	for changed := true; changed; {
		changed = false
		for key, indexes := range holders {
			left := 0
			for _, i := range indexes {
				if !dropped[i] {
					left++
				}
			}
			if left == 0 || left == backers[key] {
				continue
			}
			for _, i := range indexes {
				if !dropped[i] {
					dropped[i], changed = true, true
				}
			}
		}
	}
	// Link the bundles sharing a pack, through every pack they share.
	parent := make([]int, len(tier))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	for _, indexes := range holders {
		for _, i := range indexes[1:] {
			parent[find(i)] = find(indexes[0])
		}
	}
	members := map[int][]int{}
	for i := range tier {
		if !dropped[i] {
			members[find(i)] = append(members[find(i)], i)
		}
	}
	out := make([]Action, 0, len(tier))
	for i, action := range tier {
		if dropped[i] {
			continue
		}
		for _, other := range members[find(i)] {
			if other != i {
				action.Together = append(action.Together, ActionKey(tier[other]))
			}
		}
		out = append(out, action)
	}
	return out
}

// together is ordered[i] and the actions it must go with (Together), in
// order, ok false when one of them isn't in ordered or is already taken.
// They go at the turn of the last of them in the order, the most valued:
// none goes before its own turn. A torrent shared between them goes with
// the first only, and the later ones don't count its own files again.
func together(ordered []Action, i int, used []bool) ([]Action, []int, bool) {
	if used[i] {
		return nil, nil, false
	}
	if len(ordered[i].Together) == 0 {
		return []Action{ordered[i]}, []int{i}, true
	}
	wanted := map[string]bool{ActionKey(ordered[i]): true}
	for _, key := range ordered[i].Together {
		wanted[key] = true
	}
	var group []Action
	var indexes []int
	for j, action := range ordered {
		if wanted[ActionKey(action)] {
			if used[j] {
				return nil, nil, false
			}
			delete(wanted, ActionKey(action))
			group, indexes = append(group, action), append(indexes, j)
		}
	}
	if len(wanted) > 0 || indexes[len(indexes)-1] != i {
		return nil, nil, false
	}
	seen := map[string]bool{}
	for g := range group {
		action := group[g]
		var torrents []model.Torrent
		for _, t := range action.Torrents {
			if seen[torrentKey(t)] {
				action.ReclaimableBytes = max(action.ReclaimableBytes-t.ReclaimableBytes, 0)
				continue
			}
			seen[torrentKey(t)] = true
			torrents = append(torrents, t)
		}
		action.Torrents = torrents
		group[g] = action
	}
	return group, indexes, true
}

// filterExcluded drops any action whose ActionKey is in excluded, as if
// it were never eligible in the first place — the rest of Build's
// selection logic is unaware exclusion even happened, so a different
// candidate naturally fills whatever share of NeedBytes the excluded one
// would have.
func filterExcluded(tier []Action, excluded map[string]bool) []Action {
	if len(excluded) == 0 {
		return tier
	}
	out := make([]Action, 0, len(tier))
	for _, action := range tier {
		if excluded[ActionKey(action)] {
			continue
		}
		out = append(out, action)
	}
	return out
}

// order puts lone torrents first, since they back nothing in the library:
// the one inactive the longest first, the larger first on a tie. Then
// titles and seasons, lowest Overfin score first, the larger first on a
// tie.
// Returns a new slice.
func order(mediaTier, torrentTier []Action) []Action {
	torrents := append([]Action(nil), torrentTier...)
	media := append([]Action(nil), mediaTier...)
	sort.SliceStable(torrents, func(i, j int) bool {
		if torrents[i].LastActivity != torrents[j].LastActivity {
			return torrents[i].LastActivity < torrents[j].LastActivity
		}
		return torrents[i].ReclaimableBytes > torrents[j].ReclaimableBytes
	})
	sort.SliceStable(media, func(i, j int) bool {
		if media[i].Value != media[j].Value {
			return media[i].Value < media[j].Value
		}
		return media[i].ReclaimableBytes > media[j].ReclaimableBytes
	})
	return append(torrents, media...)
}

// enforceSeasonOrder makes a show's earlier seasons go before its later
// ones, whatever their scores: a show's most recent seasons are kept the
// longest. Each show's seasons keep the places they hold in the order, but
// fill them by season number. It must run on the whole order, before the
// plan stops at the space it needs: run after, it couldn't bring in an
// earlier season the cutoff left out.
func enforceSeasonOrder(actions []Action) {
	positionsByShow := map[string][]int{}
	for index, action := range actions {
		if action.Season == nil {
			continue
		}
		key := fmt.Sprintf("%s:%s:%d", action.Media.Type, action.Media.ServiceID, action.Media.SourceID)
		positionsByShow[key] = append(positionsByShow[key], index)
	}
	for _, positions := range positionsByShow {
		if len(positions) < 2 {
			continue
		}
		seasons := make([]Action, len(positions))
		for i, position := range positions {
			seasons[i] = actions[position]
		}
		sort.SliceStable(seasons, func(i, j int) bool { return seasons[i].Season.Number < seasons[j].Season.Number })
		for i, position := range positions {
			actions[position] = seasons[i]
		}
	}
}

// Enough is what a plan takes of the actions a caller allows, from its
// Ordered ones: what's left out then doesn't count toward the need.
func (plan Plan) Enough(allowed []Action) []Action {
	if plan.Folders != nil {
		return byShares(allowed, plan.NeedBytes, plan.Folders)
	}
	return takeUntil(allowed, plan.NeedBytes)
}

// byShares takes, in a disk whose space is shared between its root
// folders: first what has no folder (lone torrents, which come first in
// the order) as takeUntil does, then one title at a time from the folder
// furthest over its share, in bytes, counting the folders again after each:
// the first in order of that folder. It stops when the disk has what it
// needs, or no folder is over its share.
func byShares(ordered []Action, need uint64, folders []Folder) []Action {
	var taken []Action
	var freed int64
	held := map[string]int64{}
	share := map[string]int64{}
	for _, folder := range folders {
		held[folder.Path], share[folder.Path] = int64(folder.Bytes), int64(folder.Share)
	}
	used := make([]bool, len(ordered))
	for i, action := range ordered {
		if freed >= int64(need) {
			return taken
		}
		if action.Folder == "" && action.Kind == StandaloneTorrent {
			taken, freed, used[i] = append(taken, action), freed+action.ReclaimableBytes, true
		}
	}
	tried := make([]bool, len(ordered))
	emptied := map[string]bool{}
	for freed < int64(need) {
		furthest, over := "", int64(0)
		for _, folder := range folders {
			if gap := held[folder.Path] - share[folder.Path]; gap > over && !emptied[folder.Path] {
				furthest, over = folder.Path, gap
			}
		}
		if furthest == "" {
			break
		}
		next := -1
		for i, action := range ordered {
			if !used[i] && !tried[i] && action.Folder == furthest {
				next = i
				break
			}
		}
		if next < 0 {
			emptied[furthest] = true // nothing left to take there
			continue
		}
		tried[next] = true
		group, indexes, ok := together(ordered, next, used)
		if !ok {
			continue
		}
		for g, action := range group {
			used[indexes[g]] = true
			taken = append(taken, action)
			freed += action.ReclaimableBytes
			held[action.Folder] -= action.ReclaimableBytes
		}
	}
	return taken
}

// inFolders sets each title's action's root folder, among the folders.
func inFolders(ordered []Action, folders []Folder) {
	of := func(path string) string {
		path = filepath.Clean(path)
		best := ""
		for _, folder := range folders {
			if (path == folder.Path || strings.HasPrefix(path, folder.Path+string(filepath.Separator))) && len(folder.Path) > len(best) {
				best = folder.Path
			}
		}
		return best
	}
	for i := range ordered {
		if path := ordered[i].Media.Path; path != "" {
			ordered[i].Folder = of(path)
		}
	}
}

// takeUntil takes actions in order until they reclaim need bytes, an
// action with the others it goes with (together).
func takeUntil(ordered []Action, need uint64) []Action {
	var taken []Action
	var selectedBytes int64
	used := make([]bool, len(ordered))
	for i := range ordered {
		if selectedBytes >= int64(need) {
			break
		}
		group, indexes, ok := together(ordered, i, used)
		if !ok {
			continue
		}
		for g, action := range group {
			used[indexes[g]] = true
			taken = append(taken, action)
			selectedBytes += action.ReclaimableBytes
		}
	}
	return taken
}

// Build uses Target as the sole storage-reclamation threshold. Critical is
// carried in the response for configuration compatibility, but belongs to a
// future emergency/alarm policy and never gates cleanup planning.
//
// otherBytes is real disk usage outside Overtide's own view — other services
// sharing the same pool (see inventory.StorageDevice.OtherBytes) — and is
// set aside from both sides of the usage calculation: targetUsage/
// criticalUsage are evaluated against UsableBytes (TotalBytes-otherBytes),
// not the raw disk total, so a target of 90% means 90% of what Overtide
// actually has to work with, not 90% of a disk other services already have
// a share of.
//
// Candidates are taken in order (see order) until the device is back under
// its target. excluded is a set of ActionKey values to leave out of selection
// entirely, as if the candidates they name didn't exist — used for a
// live "what would replace this" recompute from the cleanup-plan
// overlay (excluding an item from one run, or one just kept but not yet
// reflected in Media/Torrents' own Protected field) without waiting for anything to actually change on disk. Every
// other caller passes nil.
//
// folders, when not nil, are the disk's root folders sharing its space:
// see byShares.
func Build(path string, otherBytes uint64, targetUsage, criticalUsage float64, media []model.Media, torrents []model.Torrent, reliable bool, excluded map[string]bool, folders []Folder) (Plan, error) {
	plan := Plan{Path: path, TargetUsagePercent: targetUsage, CriticalUsagePercent: criticalUsage, Reliable: reliable}
	var filesystemStats syscall.Statfs_t
	if err := syscall.Statfs(path, &filesystemStats); err != nil {
		plan.Message = "Storage unavailable; cleanup planning disabled"
		plan.Error = err.Error()
		return plan, fmt.Errorf("storage path %q: %w", path, err)
	}
	totalBytes := filesystemStats.Blocks * uint64(filesystemStats.Bsize)
	if totalBytes == 0 {
		err := fmt.Errorf("filesystem reports zero total capacity")
		plan.Message = "Storage unavailable; cleanup planning disabled"
		plan.Error = err.Error()
		return plan, fmt.Errorf("storage path %q: %w", path, err)
	}
	plan.Available = true
	plan.TotalBytes = totalBytes
	freeBytes := filesystemStats.Bavail * uint64(filesystemStats.Bsize)
	usedBytes := totalBytes - freeBytes
	plan.FreeBytes = freeBytes
	plan.UsedBytes = usedBytes
	// otherBytes comes from a separate, independently timed stat pass
	// (inventory.StorageDevices) — clamp rather than trust it can never
	// exceed this call's own totalBytes/usedBytes.
	if otherBytes > totalBytes {
		otherBytes = totalBytes
	}
	usableBytes := totalBytes - otherBytes
	overtideUsedBytes := usedBytes
	if otherBytes > usedBytes {
		overtideUsedBytes = 0
	} else {
		overtideUsedBytes = usedBytes - otherBytes
	}
	plan.OtherBytes = otherBytes
	plan.UsableBytes = usableBytes
	plan.OvertideUsedBytes = overtideUsedBytes
	usagePercent := 0.0
	if usableBytes > 0 {
		usagePercent = float64(overtideUsedBytes) / float64(usableBytes) * 100
	}
	plan.UsagePercent = usagePercent
	if usagePercent <= targetUsage {
		plan.Message = fmt.Sprintf("No cleanup: %.2f%% used (target %.1f%%)", usagePercent, targetUsage)
		return plan, nil
	}
	targetUsedBytes := uint64(float64(usableBytes) * targetUsage / 100)
	if overtideUsedBytes > targetUsedBytes {
		plan.NeedBytes = overtideUsedBytes - targetUsedBytes
	}
	if !reliable {
		plan.Message = "Cleanup planning paused: valuation or File topology is incomplete or stale"
		return plan, nil
	}
	if plan.NeedBytes == 0 {
		plan.Message = fmt.Sprintf("No cleanup: %.2f%% used (target %.1f%%)", usagePercent, targetUsage)
		return plan, nil
	}
	mediaTier, torrentTier := classify(media, torrents)
	if len(excluded) > 0 {
		mediaTier = filterExcluded(mediaTier, excluded)
		torrentTier = filterExcluded(torrentTier, excluded)
	}
	ordered := order(mediaTier, torrentTier)
	enforceSeasonOrder(ordered)
	final := takeUntil(ordered, plan.NeedBytes)
	if folders != nil {
		plan.Folders = folders
		inFolders(ordered, folders)
		final = byShares(ordered, plan.NeedBytes, folders)
	}
	plan.Actions = final
	plan.Ordered = ordered
	for _, action := range final {
		plan.SelectedBytes += uint64(action.ReclaimableBytes)
	}
	plan.Message = fmt.Sprintf("Need to reclaim %s to reach %.1f%% usage", Human(plan.NeedBytes), targetUsage)
	return plan, nil
}

func Human(bytes uint64) string {
	const unitSize = 1024
	value := float64(bytes)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	unitIndex := 0
	for value >= unitSize && unitIndex < len(units)-1 {
		value /= unitSize
		unitIndex++
	}
	return fmt.Sprintf("%.1f %s", value, units[unitIndex])
}
