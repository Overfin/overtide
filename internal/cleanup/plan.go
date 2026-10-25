package cleanup

import (
	"fmt"
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
	// Value is what orders actions of the same kind: a title's (or
	// season's) Overfin score, or a lone torrent's Torrent Value. Lone
	// torrents always come before titles, whatever their values.
	Value            float64        `json:"value"`
	ReclaimableBytes int64          `json:"reclaimableBytes"`
	Reasons          []model.Reason `json:"reasons,omitempty"`
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
	TargetUsagePercent   float64 `json:"targetUsagePercent"`
	CriticalUsagePercent float64 `json:"criticalUsagePercent"`
	Reliable             bool    `json:"reliable"`
	Available            bool    `json:"available"`
	Path                 string  `json:"path"`
	TotalBytes           uint64  `json:"totalBytes"`
	UsedBytes            uint64  `json:"usedBytes"`
	FreeBytes            uint64  `json:"freeBytes"`
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
	bundledHashes := map[string]bool{}

	for _, m := range media {
		if m.Type == model.Series && len(m.Seasons) > 0 {
			for _, season := range m.Seasons {
				hardlinked := m.CurrentHardlinkedTorrentsForSeason(season.Number)
				if len(hardlinked) > 0 {
					for _, t := range hardlinked {
						bundledHashes[strings.ToLower(t.Hash)] = true
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
				bundledHashes[strings.ToLower(t.Hash)] = true
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

	for _, t := range torrents {
		if t.Protected || t.RemovalRestricted {
			continue
		}
		if model.NormalizeTorrentStatus(t.AssociationStatus) == model.TorrentCurrent {
			if bundledHashes[strings.ToLower(t.Hash)] {
				continue // already represented inside (or locked out by) a bundle above
			}
			if !t.MediaHardlinkKnown {
				// Hardlink status unproven: never recommend removing it alone,
				// since it might silently free ~0 bytes while destroying real
				// swarm value, the same risk a proven hardlink carries.
				continue
			}
			if t.MediaHardlinked {
				continue // proven hardlink, but its media wasn't in this device's list; stay conservative
			}
			// Current, proven not hardlinked: an independent copy.
		}
		if !t.ReclaimableKnown || t.ReclaimableBytes <= 0 {
			continue
		}
		torrentTier = append(torrentTier, Action{Kind: StandaloneTorrent, Torrents: []model.Torrent{t}, Value: t.TorrentValue, ReclaimableBytes: t.ReclaimableBytes, Reasons: t.TorrentValueReasons})
	}

	return mediaTier, torrentTier
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

// order puts lone torrents first, lowest Torrent Value first, since they
// back nothing in the library, then titles and seasons, lowest Overfin
// score first, the larger first on a tie. Returns a new slice.
func order(mediaTier, torrentTier []Action) []Action {
	torrents := append([]Action(nil), torrentTier...)
	sort.SliceStable(torrents, func(i, j int) bool { return torrents[i].Value < torrents[j].Value })
	media := append([]Action(nil), mediaTier...)
	sort.SliceStable(media, func(i, j int) bool {
		if media[i].Value != media[j].Value {
			return media[i].Value < media[j].Value
		}
		return media[i].ReclaimableBytes > media[j].ReclaimableBytes
	})
	return append(torrents, media...)
}

// enforceSeasonOrder is a safety net on top of season Retention Value
// (which now scores recency from each season's own last-aired date, not
// import time): it guarantees that within one show, an earlier season is
// never removed after a later one, even if per-season value ever ties or
// inverts — missing/bad air-date data, or a show that aired out of numeric
// order, for instance. A show's most recent content is meant to be kept
// the longest.
//
// This reassigns each show's own season actions into the exact ranked
// positions its members already occupy after the value sort above — so
// the overall cross-show removal priority (which positions belong to this
// show at all) is unchanged — but fills those positions by ascending
// season number instead of by value.
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
func Build(path string, otherBytes uint64, targetUsage, criticalUsage float64, media []model.Media, torrents []model.Torrent, reliable bool, excluded map[string]bool) (Plan, error) {
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
	var final []Action
	var selectedBytes int64
	for _, action := range order(mediaTier, torrentTier) {
		if selectedBytes >= int64(plan.NeedBytes) {
			break
		}
		final = append(final, action)
		selectedBytes += action.ReclaimableBytes
	}
	enforceSeasonOrder(final)
	plan.Actions = final
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
