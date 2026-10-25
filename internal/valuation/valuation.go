package valuation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

func daysSince(timestamp time.Time) float64 { return time.Since(timestamp).Hours() / 24 }
func clamp(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

// serviceAllowsRemoval reports whether serviceID's own service has checked
// "Allow automatic removal" — an unknown or empty serviceID fails closed,
// the same as a removed/never-configured service would.
func serviceAllowsRemoval(configuration config.Config, serviceID string) bool {
	for _, service := range configuration.Services {
		if service.ID == serviceID {
			return service.AllowAutomaticRemoval
		}
	}
	return false
}

func hasTag(tags, protectedTags []string) bool {
	for _, mediaTag := range tags {
		for _, protectedTag := range protectedTags {
			if strings.EqualFold(mediaTag, protectedTag) {
				return true
			}
		}
	}
	return false
}

// splitTags parses qBittorrent's comma-joined free-text Tags field into
// individual, trimmed tag names.
func splitTags(tags string) []string {
	if strings.TrimSpace(tags) == "" {
		return nil
	}
	parts := strings.Split(tags, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ApplyMedia assigns each title its Retention Value: Overfin's score.
// Overtide doesn't value titles itself; Overfin does (what people watch,
// ask for and like), and Overtide weighs that against storage and torrents
// (Torrent Value, kept apart). Without Overfin, nothing is scored, so
// everything is protected. Cleanup ordering is lowest value first, the
// larger title first on a tie.
func ApplyMedia(mediaItems []model.Media, configuration config.Config) {
	for mediaIndex := range mediaItems {
		mediaItem := &mediaItems[mediaIndex]
		mediaItem.RetentionValueReasons = nil
		mediaItem.Protected = false
		mediaItem.ProtectionReason = ""
		mediaItem.RemovalRestricted = !serviceAllowsRemoval(configuration, mediaItem.ServiceID)
		applyOverfinValue(mediaItem, configuration)
	}
	sort.SliceStable(mediaItems, func(leftIndex, rightIndex int) bool {
		leftMedia, rightMedia := mediaItems[leftIndex], mediaItems[rightIndex]
		if leftMedia.RetentionValue != rightMedia.RetentionValue {
			return leftMedia.RetentionValue < rightMedia.RetentionValue
		}
		return leftMedia.SizeBytes > rightMedia.SizeBytes
	})
}

// notValued is the protection of what Overfin didn't score: never removed
// automatically on a guess.
const notValued = "Not valued by Overfin"

// applyOverfinValue makes Overfin's scores the Retention Value: a film's
// score, each season's own, a series' best season's. Overfin weighs what
// people watch, ask for and like; Overtide weighs that against storage and
// torrents (Torrent Value, kept apart). Keep tags still protect; anything
// Overfin didn't score is protected, with the highest value.
func applyOverfinValue(mediaItem *model.Media, configuration config.Config) {
	if hasTag(mediaItem.Tags, configuration.Protection.KeepTags) {
		mediaItem.Protected = true
		mediaItem.ProtectionReason = "Keep tag"
		mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Keep tag", Value: strings.Join(mediaItem.Tags, ", ")})
	}
	scored := func(score float64) model.Reason {
		return model.Reason{Label: "Overfin value", Value: fmt.Sprintf("%.1f/100", score), Points: score,
			Note: "Overfin's score: what people watch, ask for and like."}
	}
	unscored := model.Reason{Label: notValued, Value: "no score", Points: 100,
		Note: "Overfin has no score for it (not in its libraries, or not matched by TMDB or TVDB ID), so it's never removed automatically."}
	if mediaItem.Type != model.Series || len(mediaItem.Seasons) == 0 {
		if mediaItem.OverfinScore == nil {
			mediaItem.RetentionValue = 100
			mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, unscored)
			if !mediaItem.Protected {
				mediaItem.Protected, mediaItem.ProtectionReason = true, notValued
			}
			return
		}
		mediaItem.RetentionValue = *mediaItem.OverfinScore
		mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, scored(*mediaItem.OverfinScore))
		return
	}
	best, found := 0.0, false
	for i := range mediaItem.Seasons {
		season := &mediaItem.Seasons[i]
		season.Protected, season.ProtectionReason = mediaItem.Protected, mediaItem.ProtectionReason
		season.RemovalRestricted = mediaItem.RemovalRestricted
		season.RetentionValueReasons = append([]model.Reason(nil), mediaItem.RetentionValueReasons...)
		score, ok := mediaItem.OverfinSeasonScores[season.Number]
		if !ok {
			season.RetentionValue = 100
			season.RetentionValueReasons = append(season.RetentionValueReasons, unscored)
			if !season.Protected {
				season.Protected, season.ProtectionReason = true, notValued
			}
			continue
		}
		season.RetentionValue = score
		season.RetentionValueReasons = append(season.RetentionValueReasons, scored(score))
		if !found || score > best {
			best, found = score, true
		}
	}
	if !found {
		mediaItem.RetentionValue = 100
		mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, unscored)
		if !mediaItem.Protected {
			mediaItem.Protected, mediaItem.ProtectionReason = true, notValued
		}
		return
	}
	mediaItem.RetentionValue = best
	mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Overfin value", Value: fmt.Sprintf("%.1f/100", best),
		Points: best, Note: "Its best season's score, from Overfin."})
}
