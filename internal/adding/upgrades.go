package adding

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"sort"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/sonarrdriver"
)

const (
	// upgradesKey keeps when upgrade searches went over the last 24 hours,
	// a list per count (countKey);
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

// seasonValue is a season's score, its series', 0 when unknown.
func (service *Service) seasonValue(ctx context.Context, cfg config.Config, serviceID string, seriesID, season int) float64 {
	value, _ := service.seasonValueAndPlace(ctx, cfg, serviceID, seriesID, season)
	return value
}

// seasonValueAndPlace is a season's score (its series') and its place
// among its series' seasons, 1 the most valued; 0 and 0 when unknown.
func (service *Service) seasonValueAndPlace(ctx context.Context, cfg config.Config, serviceID string, seriesID, season int) (float64, int) {
	values := service.libraryValues(ctx, cfg)
	items, _, err := service.inv.Snapshot()
	if err != nil {
		return 0, 0
	}
	for _, m := range items {
		if m.ServiceID == serviceID && m.Type == model.Series && m.SourceID == seriesID {
			return values.Seasons[m.TVDBID][season], values.Places[m.TVDBID][season]
		}
	}
	return 0, 0
}

// byValue orders what's most valued first: by score, then a series'
// seasons by their place in it (a film, without one, before).
func byValue(valueI float64, placeI int, valueJ float64, placeJ int) bool {
	if valueI != valueJ {
		return valueI > valueJ
	}
	return placeI < placeJ
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
	place     int // a season's place in its series, 1 the most valued
}

func (c upgradeCandidate) key() string {
	return fmt.Sprintf("%s|%s|%d|%d", c.serviceID, c.kind, c.arrID, c.season)
}

// upgradeCandidates lists what's below its cutoff among the titles Overtide
// manages, most valued first, from the kept list (belowCutoff; wait: as
// there). ok is false while none is kept yet.
func (service *Service) upgradeCandidates(ctx context.Context, cfg config.Config, wait bool) ([]upgradeCandidate, bool, error) {
	cutoff, ok, err := service.belowCutoff(ctx, cfg, wait)
	if err != nil || !ok {
		return nil, ok, err
	}
	items, _, err := service.inv.Snapshot()
	if err != nil {
		return nil, true, err
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
		for _, id := range cutoff.Films[svc.ID] {
			if m, ok := known[fmt.Sprintf("%s|%s|%d", svc.ID, model.Movie, id)]; ok {
				out = append(out, upgradeCandidate{serviceID: svc.ID, kind: model.Movie, arrID: id, title: m.Title, path: m.Path, value: values.Films[m.TMDBID]})
			}
		}
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		for _, s := range cutoff.Seasons[svc.ID] {
			m, ok := known[fmt.Sprintf("%s|%s|%d", svc.ID, model.Series, s.SeriesID)]
			if ok && s.Season > 0 {
				out = append(out, upgradeCandidate{serviceID: svc.ID, kind: model.Series, arrID: s.SeriesID, season: s.Season, title: m.Title, path: m.Path,
					value: values.Seasons[m.TVDBID][s.Season], place: values.Places[m.TVDBID][s.Season]})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return byValue(out[i].value, out[i].place, out[j].value, out[j].place) })
	return out, true, nil
}

// searchUpgrades searches for better copies, most valued first, within the
// upgrades a day and budget: the seasons that came complete without their
// whole-season pack (a pack sought), and what's below its quality cutoff,
// each at most once in UpgradeAgain: monitored for its search, unmonitored
// right after, so Radarr and Sonarr don't go on upgrading it on their own.
// Seasons still airing, or lacking episodes, wait. In dry run, nothing goes.
// It returns the searches it asked for.
func (service *Service) searchUpgrades(ctx context.Context, cfg config.Config, entries []Entry, budget int, dryRun bool) (int, error) {
	if budget <= 0 || dryRun || !service.anyUpgradeRoom(cfg) {
		return 0, nil
	}
	room, used := budget, 0
	packs, err := service.sonarr.DuePacks()
	if err != nil {
		return 0, err
	}
	searched, err := service.upgradesSearched()
	if err != nil {
		return 0, err
	}
	candidates, _, err := service.upgradeCandidates(ctx, cfg, true)
	if err != nil {
		return 0, err
	}
	// One queue, by value: a pack goes before a lesser title, not before all.
	type job struct {
		value float64
		place int
		pack  *sonarrdriver.DuePack
		c     upgradeCandidate
	}
	var queue []job
	for i := range packs {
		p := &packs[i]
		value, place := service.seasonValueAndPlace(ctx, cfg, p.ServiceID, p.SeriesID, p.Season)
		queue = append(queue, job{value: value, place: place, pack: p})
	}
	for _, c := range candidates {
		queue = append(queue, job{value: c.value, place: c.place, c: c})
	}
	sort.SliceStable(queue, func(i, j int) bool { return byValue(queue[i].value, queue[i].place, queue[j].value, queue[j].place) })
	// What removal takes isn't upgraded: sorted, the queue ends there.
	if line, ok := service.removalLine(); ok {
		queue = slices.DeleteFunc(queue, func(j job) bool { return j.value <= line })
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
	for _, j := range queue {
		if used >= room || ctx.Err() != nil || service.trackersDown != "" {
			break
		}
		if j.pack != nil {
			went, err := service.sonarr.PackUpgrade(ctx, cfg, *j.pack)
			if went {
				used++
			}
			if err != nil {
				return used, err
			}
			continue
		}
		c := j.c
		if _, out := removed.Upgrades[c.key()]; out {
			continue // removed by the operator
		}
		if at, ok := searched[c.key()]; ok && service.now().Sub(at) < UpgradeAgain {
			continue
		}
		if moving[fmt.Sprintf("%s|%s|%d", c.serviceID, c.kind, c.arrID)] || !service.upgradeRoom(cfg, c.path) || disk(cfg, c.path, false) != "" {
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
		if err := service.recordNow(countKey(cfg, upgradesKey, service.rootOf(c.path)), 1); err != nil {
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
	// season that came complete without one.
	Why string
	// Value is Overfin's score of it, 0 when unknown; LastSearched, its
	// last upgrade search; Next, when it may be searched again (zero:
	// at the next run with room for it).
	Value        float64
	place        int // a season's in its series, for the order
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
		value, place := service.seasonValueAndPlace(ctx, cfg, p.ServiceID, p.SeriesID, p.Season)
		row := UpgradeRow{Ref: sonarrdriver.PackRef(p.ServiceID, p.SeriesID, p.Season), Title: p.Title, Kind: model.Series, Why: "Whole-season pack",
			Value: value, place: place}
		if p.NextTry.After(service.now()) {
			row.Next = p.NextTry
		}
		out = append(out, row)
	}
	// The kept list, read again behind the page when old (CutoffAt).
	candidates, _, err := service.upgradeCandidates(ctx, cfg, false)
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
		row := UpgradeRow{Ref: c.key(), Title: title, Kind: c.kind, Why: "Below its quality cutoff", Value: c.value, place: c.place}
		if at, ok := searched[c.key()]; ok {
			row.LastSearched, row.Next = at, at.Add(UpgradeAgain)
		}
		out = append(out, row)
	}
	// One order, by value, as searchUpgrades goes, without what removal
	// takes.
	sort.SliceStable(out, func(i, j int) bool { return byValue(out[i].Value, out[i].place, out[j].Value, out[j].place) })
	if line, ok := service.removalLine(); ok {
		out = slices.DeleteFunc(out, func(row UpgradeRow) bool { return row.Value <= line })
	}
	return out, nil
}

// RemovalLine is removalLine, for the Upgrades page.
func (service *Service) RemovalLine() (float64, bool) { return service.removalLine() }

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

// upgradeRoom says whether one more upgrade search may go for what's at
// path, under its root folder's upgrades a day.
func (service *Service) upgradeRoom(cfg config.Config, path string) bool {
	root := service.rootOf(path)
	times, err := service.lastDay(countKey(cfg, upgradesKey, root))
	return err == nil && len(times) < cfg.UpgradesPerDayFor(root)
}

// anyUpgradeRoom says whether a root folder (or one not known, under the
// default) may have an upgrade search now.
func (service *Service) anyUpgradeRoom(cfg config.Config) bool {
	if service.upgradeRoom(cfg, "") {
		return true
	}
	for _, root := range service.inv.RootFolders() {
		if service.upgradeRoom(cfg, root) {
			return true
		}
	}
	return false
}

// removalWindow is how far back the removal line looks.
const removalWindow = 30 * 24 * time.Hour

// removalLine is the highest value removal took in removalWindow: a title
// valued that or less isn't upgraded, since it's what removal takes, and a
// bigger copy would only cost the titles below it room. ok is false before
// removal took anything.
func (service *Service) removalLine() (float64, bool) {
	line, ok, err := service.inv.Store().RemovalLine(service.now().Add(-removalWindow))
	if err != nil {
		log.Printf("[adding] the removal line: %v", err)
		return 0, false
	}
	return line, ok
}
