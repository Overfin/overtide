package model

import "strings"

// Adding's tags in Radarr and Sonarr: each marks a title adding manages,
// and says where it came from.
const (
	// TagAdded: Overtide added it, from Overfin's suggestions.
	TagAdded = "overtide-added"
	// TagRequested: Overtide added it, requested in Overfin.
	TagRequested = "overtide-requested"
	// TagFound: it was there before Overtide took it, when "Manage all"
	// was switched on.
	TagFound = "overtide-found"
	// TagNew: added from outside (a list, a request, by hand) while
	// "Manage all" was on.
	TagNew = "overtide-new"
)

// TagIgnore is set by the operator, in Radarr or Sonarr: a title
// Overtide never takes, tags, unmonitors or searches for.
const TagIgnore = "overtide-ignore"

// The tags' names when adding was a separate app, Overswell (2026-10-30 to
// 11-05): adding replaces them once (see LegacyTags), and reads them as the
// new ones until then (see sameTag).
var formerNames = map[string]string{
	TagAdded:     "overswell-added",
	TagRequested: "overswell-requested",
	TagFound:     "overswell-found",
	TagNew:       "overswell-new",
	TagIgnore:    "overswell-ignore",
}

// legacyTag is Overtide's own tag from before the split, when it also
// added titles: it's replaced by where the title came from.
const legacyTag = "overtide"

// LegacyTags are the former tags and what replaces each; "overtide" is
// replaced by where the title came from ("").
var LegacyTags = []struct{ Old, New string }{
	{legacyTag, ""},
	{formerNames[TagAdded], TagAdded},
	{formerNames[TagRequested], TagRequested},
	{formerNames[TagFound], TagFound},
	{formerNames[TagNew], TagNew},
	{formerNames[TagIgnore], TagIgnore},
}

// sameTag reports whether label is tag, or the name tag had under
// Overswell.
func sameTag(label, tag string) bool {
	return strings.EqualFold(label, tag) || (formerNames[tag] != "" && strings.EqualFold(label, formerNames[tag]))
}

// ManagedTags are adding's tags.
var ManagedTags = []string{TagAdded, TagRequested, TagFound, TagNew}

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
