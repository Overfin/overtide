package model

import "strings"

// Adding's tags in Radarr and Sonarr (still named as Overswell named them:
// renaming them is a decision of its own): each marks a title adding
// manages, and says where it came from.
const (
	// TagAdded: Overtide added it, from Overfin's suggestions.
	TagAdded = "overswell-added"
	// TagRequested: Overtide added it, requested in Overfin.
	TagRequested = "overswell-requested"
	// TagFound: it was there before Overtide took it, when "Manage all"
	// was switched on.
	TagFound = "overswell-found"
	// TagNew: added from outside (a list, a request, by hand) while
	// "Manage all" was on.
	TagNew = "overswell-new"
)

// TagIgnore is set by the operator, in Radarr or Sonarr: a title
// Overtide never takes, tags, unmonitors or searches for.
const TagIgnore = "overswell-ignore"

// Overtide's tags, from when Overtide also added titles: Overswell
// replaces them with its own once (see LegacyTags).
const (
	legacyTag          = "overtide"
	legacyTagAdded     = "overtide-added"
	legacyTagRequested = "overtide-requested"
	legacyTagFound     = "overtide-found"
	legacyTagNew       = "overtide-new"
	legacyTagIgnore    = "overtide-ignore"
)

// LegacyTags are Overtide's former tags and what replaces each; the
// first, "overtide", is replaced by where the title came from ("").
var LegacyTags = []struct{ Old, New string }{
	{legacyTag, ""},
	{legacyTagAdded, TagAdded},
	{legacyTagRequested, TagRequested},
	{legacyTagFound, TagFound},
	{legacyTagNew, TagNew},
	{legacyTagIgnore, TagIgnore},
}

// ManagedTags are Overswell's tags.
var ManagedTags = []string{TagAdded, TagRequested, TagFound, TagNew}

// IsManagedTag reports whether label is one of Overswell's tags.
func IsManagedTag(label string) bool {
	for _, tag := range ManagedTags {
		if strings.EqualFold(label, tag) {
			return true
		}
	}
	return false
}

// HasLabel reports whether one of the tag IDs is the tag.
func HasLabel(ids []int, labels map[int]string, tag string) bool {
	for _, id := range ids {
		if strings.EqualFold(labels[id], tag) {
			return true
		}
	}
	return false
}

// HasManagedLabel reports whether one of the tag IDs is one of Overswell's
// tags.
func HasManagedLabel(ids []int, labels map[int]string) bool {
	for _, id := range ids {
		if IsManagedTag(labels[id]) {
			return true
		}
	}
	return false
}
