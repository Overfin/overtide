package valuation

import (
	"fmt"
	"sort"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

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

// ApplyMedia assigns each title its Retention Value: Overfin's score.
// Overtide doesn't value titles itself; Overfin does (what people watch,
// ask for and like), and Overtide weighs that against storage and torrents.
// Without Overfin, nothing is scored, so
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
// torrents. What the operator kept is
// protected; anything Overfin didn't score is protected, with the highest
// value.
func applyOverfinValue(mediaItem *model.Media, configuration config.Config) {
	if configuration.IsKept(config.KeptMediaKey(string(mediaItem.Type), mediaItem.ServiceID, mediaItem.SourceID)) {
		mediaItem.Protected = true
		mediaItem.ProtectionReason = "Kept"
		mediaItem.RetentionValueReasons = append(mediaItem.RetentionValueReasons, model.Reason{Label: "Kept", Value: "on the Storage page's list"})
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
