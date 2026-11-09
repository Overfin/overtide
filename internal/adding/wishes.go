package adding

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
)

// Wishes: what people wished for in Overfin, which Overtide fetches first,
// in the order of Overfin's turns, and whose fate it tells Overfin back. A
// wish is a request in the waiting list (FromRequest): it never expires,
// and the titles a day don't count it.

// wishesMax is how many wishes are read at once: Overfin's most.
const wishesMax = 200

// readWishes brings Overfin's wishes into the waiting list, in its turn
// order, first: a film to a Radarr, a series (Overfin knowing its TheTVDB
// ID) to a Sonarr with its wished seasons, where their destination says
// (config.DestinationFor), else the first. One Overtide
// had from Overfin's suggestions becomes a wish. A wish missing from the
// list may only not be due (not found yet, not out, downloading): it's
// dropped only once Overfin says no one wishes for it (dropGone). If the
// wishes can't be read, nothing changes.
func (service *Service) readWishes(ctx context.Context, cfg config.Config, entries []Entry) []Entry {
	if cfg.Overfin.URL == "" {
		return entries
	}
	wished, err := service.inv.OverfinClient().WithContext(ctx).Wishes(wishesMax)
	if err != nil {
		log.Printf("[adding] Overfin's wishes (its key needs \"Wishes to fetch\"): %v", err)
		return entries
	}
	radarrs, sonarrs := cfg.ServicesOfType("radarr"), cfg.ServicesOfType("sonarr")
	byID := map[string]int{}
	for i, entry := range entries {
		byID[entry.id()] = i
	}
	now := service.now()
	seen := map[string]bool{}
	for position, m := range wished {
		entry := Entry{Kind: m.Kind, Title: m.Name, Source: FromRequest, Position: position, WishTMDB: m.TMDB, Categories: m.Categories}
		switch {
		case m.Kind == model.Movie && len(radarrs) > 0:
			entry.Key, entry.ServiceID, entry.ExternalID = movieKey(m.TMDB), radarrs[0].ID, m.TMDB
			if d, ok := cfg.DestinationFor("radarr", m.Categories); ok {
				entry.ServiceID = d.ServiceID
			}
		case m.Kind == model.Series && len(sonarrs) > 0 && m.TVDB > 0:
			entry.Key, entry.ServiceID, entry.ExternalID = seriesKey(m.TVDB), sonarrs[0].ID, m.TVDB
			if d, ok := cfg.DestinationFor("sonarr", m.Categories); ok {
				entry.ServiceID = d.ServiceID
			}
			entry.Seasons = m.Seasons
		default:
			if m.Kind == model.Series && m.TVDB == 0 {
				log.Printf("[adding] wish %q: Overfin knows no TheTVDB ID for it, which Sonarr needs", m.Name)
			}
			continue
		}
		seen[entry.id()] = true
		if i, ok := byID[entry.id()]; ok {
			old := &entries[i]
			old.Source, old.Position, old.WishTMDB = FromRequest, position, m.TMDB
			// Imported a while ago and wished for again: it left (removed
			// since), or seasons are new. Overfin only lists what it lacks,
			// and has had time to see what arrived.
			if old.State == Imported && now.Sub(old.UpdatedAt) > seenByOverfin {
				old.State, old.Tries, old.NextTry, old.UpdatedAt = Waiting, 0, now, now
				old.Result, old.Reported, old.Unmonitored = "Wished for again", "", false
			}
			// A series waiting gets the seasons wished now as well.
			if old.waiting() {
				for _, n := range entry.Seasons {
					if !slices.Contains(old.Seasons, n) {
						old.Seasons = append(old.Seasons, n)
					}
				}
				slices.Sort(old.Seasons)
			}
			continue
		}
		entry.State, entry.NextTry, entry.AddedAt, entry.UpdatedAt = Waiting, now, now, now
		byID[entry.id()] = len(entries)
		entries = append(entries, entry)
	}
	// A wish waiting that Overfin didn't list is told again: Overfin
	// answers whether anyone still wishes for it (reportWishes).
	for i := range entries {
		if e := &entries[i]; e.Source == FromRequest && e.waiting() && !seen[e.id()] {
			e.Reported = ""
		}
	}
	return entries
}

// seenByOverfin is how long Overfin may take to see a title imported, its
// library read again; before that, a wish it still lists says nothing.
const seenByOverfin = 24 * time.Hour

// wishState is what to tell Overfin of a wish: downloading, not out yet
// (with its release day), not found yet (with its next try) or waiting;
// "" once imported (Overfin sees it arrive itself).
func wishState(entry Entry) (state string, nextTry time.Time, release string) {
	switch {
	case entry.State == Downloading:
		return overfin.WishDownloading, time.Time{}, ""
	case entry.State == NotReleased:
		if !entry.ReleaseAt.IsZero() {
			release = entry.ReleaseAt.UTC().Format(time.DateOnly)
		}
		return overfin.WishUnreleased, time.Time{}, release
	case entry.State == Waiting && entry.Tries > 0:
		return overfin.WishNotFound, entry.NextTry, ""
	case entry.State == Waiting:
		return overfin.WishWaiting, time.Time{}, ""
	}
	return "", time.Time{}, ""
}

// reportWishes tells Overfin what became of each wish whose state changed
// since it was last told (reportWish); one no one wishes for any more
// leaves the list while it still waits. Never in dry run, which changes
// nothing.
func (service *Service) reportWishes(ctx context.Context, cfg config.Config, entries []Entry, dryRun bool) []Entry {
	kept := entries[:0]
	for _, entry := range entries {
		if service.reportWish(ctx, cfg, &entry, dryRun) && entry.waiting() {
			continue // no one wishes for it any more
		}
		kept = append(kept, entry)
	}
	return kept
}

// reportWish tells Overfin what became of a wish, if that changed since it
// was last told: each season of a series on its own. It reports whether no
// one wishes for it any more.
func (service *Service) reportWish(ctx context.Context, cfg config.Config, entry *Entry, dryRun bool) (gone bool) {
	if dryRun || cfg.Overfin.URL == "" || entry.Source != FromRequest || entry.WishTMDB == 0 {
		return false
	}
	state, next, release := wishState(*entry)
	told := fmt.Sprintf("%s %s %s", state, next.UTC().Format(time.RFC3339), release)
	if state == "" || told == entry.Reported {
		return false
	}
	client := service.inv.OverfinClient().WithContext(ctx)
	gone = true
	failed := false
	tell := func(season *int) {
		switch err := client.WishStatus(entry.WishTMDB, season, state, next, release); {
		case errors.Is(err, overfin.ErrNotWished):
		case err != nil:
			log.Printf("[adding] telling Overfin about wish %q: %v", entry.Title, err)
			failed, gone = true, false
		default:
			gone = false
		}
	}
	if entry.Kind == model.Series {
		for _, n := range entry.Seasons {
			tell(&n)
		}
	} else {
		tell(nil)
	}
	if !failed {
		entry.Reported = told
	}
	return gone
}
