package model

import "strings"

// Adding's tags in Radarr and Sonarr: each marks a title adding manages,
// and says where it came from.
const (
	// TagAdded: Overtide added it, from Overfin's suggestions.
	TagAdded = "overtide-added"
	// TagRequested: Overtide added it, requested in Overfin.
	TagRequested = "overtide-requested"
	// TagFound: "Manage all" took it: it was in Radarr or Sonarr, added
	// by the operator, an import list or another app. Overtide manages it
	// only while "Manage all" is on.
	TagFound = "overtide-found"
)

// TagIgnore is set by the operator, in Radarr or Sonarr: a title
// Overtide never takes, tags, unmonitors or searches for.
const TagIgnore = "overtide-ignore"

// sameTag reports whether label is tag, case aside.
func sameTag(label, tag string) bool {
	return strings.EqualFold(label, tag)
}

// ManagedTags are adding's tags: what Overtide added (OwnTags), and what
// "Manage all" took (TagFound).
var ManagedTags = []string{TagAdded, TagRequested, TagFound}

// OwnTags are the tags of what Overtide added itself: all it manages
// unless "Manage all" is on.
var OwnTags = []string{TagAdded, TagRequested}

// IsOwnTag reports whether label marks a title Overtide added itself.
func IsOwnTag(label string) bool {
	for _, tag := range OwnTags {
		if sameTag(label, tag) {
			return true
		}
	}
	return false
}

// HasOwnLabel reports whether one of the tag IDs marks a title Overtide
// added itself.
func HasOwnLabel(ids []int, labels map[int]string) bool {
	for _, id := range ids {
		if IsOwnTag(labels[id]) {
			return true
		}
	}
	return false
}

// IsManagedTag reports whether label is one of adding's tags.
func IsManagedTag(label string) bool {
	for _, tag := range ManagedTags {
		if sameTag(label, tag) {
			return true
		}
	}
	return false
}

// HasLabel reports whether one of the tag IDs is the tag.
func HasLabel(ids []int, labels map[int]string, tag string) bool {
	for _, id := range ids {
		if sameTag(labels[id], tag) {
			return true
		}
	}
	return false
}

// HasManagedLabel reports whether one of the tag IDs is one of adding's
// tags.
func HasManagedLabel(ids []int, labels map[int]string) bool {
	for _, id := range ids {
		if IsManagedTag(labels[id]) {
			return true
		}
	}
	return false
}
