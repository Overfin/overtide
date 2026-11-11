package adding

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/sonarrdriver"
)

const (
	// upgradesKey keeps when upgrade searches went over the last 24 hours;
	// upgradedKey, when each title was last searched for one.
	upgradesKey = "adding.upgrades"
	upgradedKey = "adding.upgraded"
	// UpgradeAgain is how long before a title is searched for an upgrade
	// again.
	UpgradeAgain = 30 * 24 * time.Hour
)

// libraryValues are the library's scores, read once a run. If Overfin can't
// say, every title counts the same.
func (service *Service) libraryValues(ctx context.Context, cfg config.Config) overfin.Values {
	if service.values != nil {
		return *service.values
	}
	values := overfin.Values{}
	if cfg.Overfin.URL != "" {
		var err error
		if values, err = service.inv.OverfinClient().WithContext(ctx).LibraryValues(); err != nil {
			log.Printf("[adding] the library's values, from Overfin (its key needs \"Values\"): %v", err)
		}
	}
	service.values = &values
	return values
}

// seasonValue is a season's score, 0 when unknown.
func (service *Service) seasonValue(ctx context.Context, cfg config.Config, serviceID string, seriesID, season int) float64 {
	values := service.libraryValues(ctx, cfg)
	items, _, err := service.inv.Snapshot()
	if err != nil {
		return 0
	}
	for _, m := range items {
		if m.ServiceID == serviceID && m.Type == model.Series && m.SourceID == seriesID {
			return values.Seasons[m.TVDBID][season]
		}
	}
	return 0
}

// upgradeCandidate is a film or a season below its quality cutoff.
type upgradeCandidate struct {
	serviceID string
	kind      model.MediaType
	arrID     int
	season    int
	title     string
	path      string
	value     float64
}

func (c upgradeCandidate) key() string {
	return fmt.Sprintf("%s|%s|%d|%d", c.serviceID, c.kind, c.arrID, c.season)
}

// upgradeCandidates lists what's below its cutoff among the titles Overtide
// manages, most valued first.
func (service *Service) upgradeCandidates(ctx context.Context, cfg config.Config) ([]upgradeCandidate, error) {
	items, _, err := service.inv.Snapshot()
	if err != nil {
		return nil, err
	}
	known := map[string]model.Media{}
	for _, m := range items {
		if service.managed(m) && !model.HasTag(m.Tags, model.TagIgnore) {
			known[fmt.Sprintf("%s|%s|%d", m.ServiceID, m.Type, m.SourceID)] = m
		}
	}
	values := service.libraryValues(ctx, cfg)
	var out []upgradeCandidate
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			return nil, err
		}
		ids, err := client.WithContext(ctx).BelowCutoff()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if m, ok := known[fmt.Sprintf("%s|%s|%d", svc.ID, model.Movie, id)]; ok {
				out = append(out, upgradeCandidate{serviceID: svc.ID, kind: model.Movie, arrID: id, title: m.Title, path: m.Path, value: values.Films[m.TMDBID]})
			}
		}
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return nil, err
		}
		seasons, err := client.WithContext(ctx).BelowCutoff()
		if err != nil {
			return nil, err
		}
		for _, s := range seasons {
			m, ok := known[fmt.Sprintf("%s|%s|%d", svc.ID, model.Series, s.SeriesID)]
			if ok && s.Season > 0 {
				out = append(out, upgradeCandidate{serviceID: svc.ID, kind: model.Series, arrID: s.SeriesID, season: s.Season, title: m.Title, path: m.Path, value: values.Seasons[m.TVDBID][s.Season]})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].value > out[j].value })
	return out, nil
}

// searchUpgrades searches for better copies of what's below its quality
// cutoff, most valued first, within the upgrades a day and budget, each
// title at most once in UpgradeAgain: monitored for its search, unmonitored
// right after, so Radarr and Sonarr don't go on upgrading it on their own.
// Seasons still airing, or lacking episodes, wait. In dry run, nothing goes.
// It returns the searches it asked for.
func (service *Service) searchUpgrades(ctx context.Context, cfg config.Config, entries []Entry, budget int, dryRun bool) (int, error) {
	limit := cfg.Adding.UpgradesPerDay
	if limit <= 0 || budget <= 0 || dryRun {
		return 0, nil
	}
	times, err := service.lastDay(upgradesKey)
	if err != nil {
		return 0, err
	}
	room := min(limit-len(times), budget)
	if room <= 0 {
		return 0, nil
	}
	// Seasons complete without their pack first: those are holes filled.
	used, err := service.sonarr.PackUpgrades(ctx, cfg, room)
	if err != nil {
		return used, err
	}
	searched, err := service.upgradesSearched()
	if err != nil {
		return used, err
	}
	candidates, err := service.upgradeCandidates(ctx, cfg)
	if err != nil {
		return used, err
	}
	moving := map[string]bool{}
	for _, entry := range entries {
		if entry.ArrID != 0 && !entry.waiting() && entry.State != Imported {
			moving[fmt.Sprintf("%s|%s|%d", entry.ServiceID, entry.Kind, entry.ArrID)] = true
		}
	}
	upgradable, err := service.sonarr.Upgradable()
	if err != nil {
		return used, err
	}
	disk := service.diskStop
	if service.disk != nil {
		disk = service.disk
	}
	removed := service.sonarr.Removed()
	for _, c := range candidates {
		if used >= room || ctx.Err() != nil || service.trackersDown != "" {
			break
		}
		if _, out := removed.Upgrades[c.key()]; out {
			continue // removed by the operator
		}
		if at, ok := searched[c.key()]; ok && service.now().Sub(at) < UpgradeAgain {
			continue
		}
		if moving[fmt.Sprintf("%s|%s|%d", c.serviceID, c.kind, c.arrID)] || disk(cfg, c.path, false) != "" {
			continue
		}
		if c.kind == model.Series {
			ok, err := upgradable(ctx, c.serviceID, c.arrID, c.season)
			if err != nil {
				return used, err
			}
			if !ok {
				continue
			}
		}
		if !service.ready() {
			break
		}
		entry := Entry{Kind: c.kind, ServiceID: c.serviceID, ArrID: c.arrID, Title: c.title}
		if c.kind == model.Series {
			entry.Seasons = []int{c.season}
		}
		a, err := service.arrFor(ctx, &entry)
		if err != nil {
			return used, err
		}
		queued, err := a.Queued(c.arrID)
		if err != nil || queued {
			continue
		}
		if err := a.Monitor(c.arrID, true); err != nil {
			log.Printf("[adding] monitor %s for its upgrade: %v", c.title, err)
			continue
		}
		service.step(c.title, a.Name+" is searching for a better copy")
		started := time.Now()
		_, searchErr := a.Search(ctx, c.arrID)
		if err := a.Monitor(c.arrID, false); err != nil {
			log.Printf("[adding] unmonitor %s after its upgrade search: %v", c.title, err)
		}
		used++
		if searchErr != nil {
			log.Printf("[adding] upgrade search of %s: %v", c.title, searchErr)
		}
		// Not counted against the title when the trackers weren't all there.
		if down, _ := service.unavailable(started); down {
			continue
		}
		searched[c.key()] = service.now()
		if err := service.recordNow(upgradesKey, 1); err != nil {
			return used, err
		}
		if err := service.saveUpgradesSearched(searched); err != nil {
			return used, err
		}
	}
	return used, nil
}

// upgradesSearched is when each title was last searched for an upgrade,
// those within UpgradeAgain.
func (service *Service) upgradesSearched() (map[string]time.Time, error) {
	out := map[string]time.Time{}
	raw, err := service.inv.Store().Meta(upgradedKey)
	if err != nil || raw == "" {
		return out, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("read the upgrades searched: %w", err)
	}
	for key, at := range out {
		if service.now().Sub(at) >= UpgradeAgain {
			delete(out, key)
		}
	}
	return out, nil
}

func (service *Service) saveUpgradesSearched(searched map[string]time.Time) error {
	encoded, err := json.Marshal(searched)
	if err != nil {
		return err
	}
	return service.inv.Store().SetMeta(upgradedKey, string(encoded))
}

// UpgradeRow is a title or a season waiting for an upgrade search, for the
// Upgrades page, in the order they're searched.
type UpgradeRow struct {
	// Ref names it for RemoveUpgrade.
	Ref   string
	Title string
	Kind  model.MediaType
	// Why: "Below its quality cutoff", or "Whole-season pack" for a
	// season that came complete without one (searched first).
	Why string
	// Value is Overfin's score of it, 0 when unknown; LastSearched, its
	// last upgrade search; Next, when it may be searched again (zero:
	// at the next run with room for it).
	Value        float64
	LastSearched time.Time
	Next         time.Time
}

// Upgrades lists what waits for an upgrade search, in their order, but
// what the operator removed.
func (service *Service) Upgrades(ctx context.Context) ([]UpgradeRow, error) {
	cfg := service.inv.Config()
	removed := service.sonarr.Removed()
	var out []UpgradeRow
	packs, err := service.sonarr.PackUpgradeRows()
	if err != nil {
		return nil, err
	}
	for _, p := range packs {
		row := UpgradeRow{Ref: sonarrdriver.PackRef(p.ServiceID, p.SeriesID, p.Season), Title: p.Title, Kind: model.Series, Why: "Whole-season pack",
			Value: service.seasonValue(ctx, cfg, p.ServiceID, p.SeriesID, p.Season)}
		if p.NextTry.After(service.now()) {
			row.Next = p.NextTry
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	candidates, err := service.upgradeCandidates(ctx, cfg)
	if err != nil {
		return nil, err
	}
	searched, err := service.upgradesSearched()
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		if _, gone := removed.Upgrades[c.key()]; gone {
			continue
		}
		title := c.title
		if c.kind == model.Series {
			title = fmt.Sprintf("%s S%d", c.title, c.season)
		}
		row := UpgradeRow{Ref: c.key(), Title: title, Kind: c.kind, Why: "Below its quality cutoff", Value: c.value}
		if at, ok := searched[c.key()]; ok {
			row.LastSearched, row.Next = at, at.Add(UpgradeAgain)
		}
		out = append(out, row)
	}
	return out, nil
}

// RemoveUpgrade keeps a title or a season from ever being searched for an
// upgrade, by its row's Ref.
func (service *Service) RemoveUpgrade(ctx context.Context, ref string) error {
	rows, err := service.Upgrades(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Ref == ref {
			return service.sonarr.Remove(func(r *sonarrdriver.Removed, now time.Time) { r.Upgrades[ref] = now })
		}
	}
	return ErrNotInList
}

// UpgradesToday counts the upgrade searches of the last 24 hours.
func (service *Service) UpgradesToday() int {
	times, _ := service.lastDay(upgradesKey)
	return len(times)
}
