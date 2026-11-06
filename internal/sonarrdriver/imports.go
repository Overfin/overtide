package sonarrdriver

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/tries"
)

const (
	// importLookback is how far back the first look goes: downloads started
	// before auto-import went off may have finished since.
	importLookback = 14 * 24 * time.Hour
	// importOverlap is read again each time, so a grab recorded late isn't
	// missed: what's known already is skipped.
	importOverlap = time.Hour
	// importGiveUp is how long a download Sonarr has no files of may stay
	// open: it left the download client, nothing can be imported from it.
	importGiveUp = 30 * 24 * time.Hour
	// importKeep is how long a finished download is remembered.
	importKeep = 60 * 24 * time.Hour
	// ordersKeep is how long what Overfin said of a series is believed.
	ordersKeep = 6 * time.Hour
)

// downloadImport is a download Overtide imports, Sonarr's auto-import being
// off.
type downloadImport struct {
	SeriesID  int       `json:"seriesId"`
	Title     string    `json:"title"`
	GrabbedAt time.Time `json:"grabbedAt"`
	// Done: nothing more is to come of it.
	Done bool `json:"done,omitempty"`
	// Failed: Sonarr says the download failed.
	Failed bool `json:"failed,omitempty"`
	// Waiting: its files nothing says which episode they are. Looked at
	// again each run, in case something does now.
	Waiting []waitingFile `json:"waiting,omitempty"`
	// Chosen: the episodes the operator said some files are, by file.
	Chosen map[string][]int `json:"chosen,omitempty"`
}

// cachedOrders is what Overfin said of a series, and when.
type cachedOrders struct {
	orders *overfin.EpisodeOrders
	at     time.Time
}

// ImportDownloads imports what Sonarr no longer does: with its auto-import
// off, each download it started, however it was started, is imported once
// finished, file by file (identify). Each run looks at everything again, so
// a file waiting goes in as soon as something says what it is. An error is
// logged and tried next run. In dry run, nothing's done.
func (driver *Driver) ImportDownloads(ctx context.Context, cfg config.Config, dryRun bool) {
	driver.autoImportOff = map[string]bool{}
	states, err := driver.States()
	if err != nil {
		log.Printf("[adding] the imports: %v", err)
		return
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := driver.inv.SonarrClient(svc.ID)
		if err != nil {
			continue
		}
		client = client.WithContext(ctx)
		on, err := client.AutoImportOn()
		if err != nil {
			log.Printf("[adding] Sonarr %s's settings: %v", svc.Name, err)
			// Not known to be off: nothing's imported, and nothing's taken
			// for gone either.
			driver.autoImportOff[svc.ID] = states[svc.ID] != nil && !states[svc.ID].ImportsOffSince.IsZero()
			continue
		}
		state := stateFor(states, svc.ID)
		if on {
			if !state.ImportsOffSince.IsZero() {
				log.Printf("[adding] Sonarr %s imports its downloads again: Overtide stops", svc.Name)
			}
			state.Imports, state.ImportsOffSince, state.ImportsRead = nil, time.Time{}, time.Time{}
			continue
		}
		driver.autoImportOff[svc.ID] = true
		if dryRun {
			continue
		}
		driver.importFrom(ctx, cfg, client, state, svc.ID, svc.Name)
	}
	if !dryRun {
		if err := driver.SaveStates(states); err != nil {
			log.Printf("[adding] the imports: %v", err)
		}
	}
}

// importFrom looks at one Sonarr's downloads.
func (driver *Driver) importFrom(ctx context.Context, cfg config.Config, client *sonarr.Client, state *State, svcID, name string) {
	now := driver.now()
	if state.ImportsOffSince.IsZero() {
		state.ImportsOffSince = now
		state.ImportsRead = now.Add(-importLookback)
		log.Printf("[adding] Sonarr %s's auto-import is off: Overtide imports its downloads", name)
	}
	if state.Imports == nil {
		state.Imports = map[string]*downloadImport{}
	}
	driver.applyChoices(client, state, svcID)
	grabs, err := client.GrabsSince(state.ImportsRead.Add(-importOverlap))
	if err != nil {
		log.Printf("[adding] Sonarr %s's grabs: %v", name, err)
		return
	}
	for _, grab := range grabs {
		if grab.DownloadID != "" && state.Imports[grab.DownloadID] == nil {
			state.Imports[grab.DownloadID] = &downloadImport{SeriesID: grab.SeriesID, Title: grab.SourceTitle, GrabbedAt: grab.Date}
		}
	}
	state.ImportsRead = now
	// What's in the queue is still going. A queue that can't be read says
	// nothing, so nothing's looked at.
	queue, err := client.FullQueue()
	if err != nil {
		log.Printf("[adding] Sonarr %s's queue: %v", name, err)
		return
	}
	going := map[string]bool{}
	for _, item := range queue {
		going[item.DownloadID] = true
	}
	titles, err := client.Titles()
	if err != nil {
		log.Printf("[adding] Sonarr %s's series: %v", name, err)
		return
	}
	series := map[int]sonarr.Title{}
	for _, title := range titles {
		series[title.ID] = title
	}
	for id, download := range state.Imports {
		if ctx.Err() != nil {
			return
		}
		if download.Done {
			if now.Sub(download.GrabbedAt) > importKeep {
				delete(state.Imports, id)
			}
			continue
		}
		if going[id] {
			continue
		}
		if err := driver.importDownload(ctx, cfg, client, state, id, download, series[download.SeriesID]); err != nil {
			log.Printf("[adding] importing %s: %v", download.Title, err)
		}
	}
}

// importDownload looks at one download that isn't in Sonarr's queue.
func (driver *Driver) importDownload(ctx context.Context, cfg config.Config, client *sonarr.Client, state *State, id string, download *downloadImport, series sonarr.Title) error {
	now := driver.now()
	events, err := client.DownloadEvents(id)
	if err != nil {
		return err
	}
	imported := map[string]bool{}
	for _, event := range events {
		switch event.EventType {
		case "downloadFailed":
			download.Done, download.Failed, download.Waiting = true, true, nil
			return nil
		case "downloadFolderImported":
			// Sonarr's own, before auto-import went off: it had its say.
			if event.Date.Before(state.ImportsOffSince) {
				download.Done, download.Waiting = true, nil
				return nil
			}
			if path := event.Data["droppedPath"]; path != "" {
				imported[path] = true
			}
		}
	}
	// A download still going has no list, and just after a restart Sonarr
	// has none of any: asked again next run, until it's clearly gone.
	files, err := client.DownloadFiles(id)
	if err != nil && !sonarr.StillGoing(err) {
		log.Printf("[adding] the files of %s: %v", download.Title, err)
	}
	if err != nil || len(files) == 0 {
		if now.Sub(download.GrabbedAt) > importGiveUp {
			log.Printf("[adding] %s: no files after %s, it left the download client", download.Title, importGiveUp)
			download.Done, download.Waiting = true, nil
		}
		return nil
	}
	// Should Sonarr answer with the series' own files, those are never
	// imported over themselves.
	library, err := client.SeriesPath(download.SeriesID)
	if err != nil {
		return err
	}
	library = strings.TrimSuffix(library, "/") + "/"
	own := files[:0:0]
	for _, file := range files {
		if library == "/" || !strings.HasPrefix(file.Path, library) {
			own = append(own, file)
		}
	}
	if len(own) == 0 {
		return nil
	}
	episodes, err := client.Episodes(download.SeriesID)
	if err != nil {
		return err
	}
	if series.SeriesType == "anime" {
		own = placeAbsolute(own, episodes, series.Title)
	}
	imports, waiting := identify(own, episodes, driver.episodeOrders(ctx, cfg, series.TMDBID), now)
	imports, waiting = operatorSays(own, imports, waiting, download.Chosen, episodes)
	var rest []sonarr.ImportItem
	for _, item := range imports {
		if imported[item.Path] {
			continue
		}
		item.DownloadID = id
		rest = append(rest, item)
	}
	waited := waiting[:0:0]
	for _, file := range waiting {
		if !imported[file.Path] {
			waited = append(waited, file)
		}
	}
	// A later release may have settled their seasons: nothing's left to do
	// with them, and nobody needs to hear about them.
	if len(waited) > 0 {
		settled, err := settledSeasons(client, download.SeriesID, episodes, download.GrabbedAt)
		if err != nil {
			return err
		}
		waited = slices.DeleteFunc(waited, func(file waitingFile) bool { return settled[fileSeason(file.Path)] })
	}
	download.Waiting = waited
	if len(rest) == 0 {
		download.Done = len(waited) == 0
		return nil
	}
	driver.Step(download.Title, fmt.Sprintf("Importing %d files", len(rest)))
	if _, err := tries.SearchAndCheck(ctx,
		func() (int, error) { return client.Import(download.SeriesID, rest) },
		client.CommandStatus,
		func() (bool, error) { return true, nil }); err != nil {
		return err
	}
	log.Printf("[adding] imported %d files of %s, %d waiting", len(rest), download.Title, len(waited))
	return nil
}

// operatorSays puts the operator's word over identify's: a file they said is
// an episode of the series goes there, and nothing else claims that episode
// meanwhile. Episodes that aren't the series' are ignored.
func operatorSays(files, imports []sonarr.ImportItem, waiting []waitingFile, chosen map[string][]int, episodes []sonarr.Episode) ([]sonarr.ImportItem, []waitingFile) {
	if len(chosen) == 0 {
		return imports, waiting
	}
	valid := map[int]bool{}
	for _, e := range episodes {
		valid[e.ID] = true
	}
	said := map[string][]sonarr.ImportEpisode{}
	taken := map[int]bool{}
	for path, ids := range chosen {
		var list []sonarr.ImportEpisode
		for _, id := range ids {
			if valid[id] {
				list = append(list, sonarr.ImportEpisode{ID: id})
				taken[id] = true
			}
		}
		if len(list) > 0 {
			said[path] = list
		}
	}
	var out []sonarr.ImportItem
	for _, item := range files {
		if list, ok := said[item.Path]; ok {
			item.Episodes = list
			out = append(out, item)
		}
	}
	for _, item := range imports {
		if _, ok := said[item.Path]; ok {
			continue
		}
		if slices.ContainsFunc(item.Episodes, func(e sonarr.ImportEpisode) bool { return taken[e.ID] }) {
			waiting = append(waiting, waitingFile{Path: item.Path, Why: "You said another file is this episode."})
			continue
		}
		out = append(out, item)
	}
	waiting = slices.DeleteFunc(waiting, func(file waitingFile) bool {
		_, ok := said[file.Path]
		return ok
	})
	return out, waiting
}

// episodeOrders is what Overfin says of a series, nil when it can't say: a
// series without a TMDB ID, or Overfin not there.
func (driver *Driver) episodeOrders(ctx context.Context, cfg config.Config, tmdbID int) *overfin.EpisodeOrders {
	if tmdbID == 0 {
		return nil
	}
	now := driver.now()
	if cached, ok := driver.ordersCache[tmdbID]; ok && now.Sub(cached.at) < ordersKeep {
		return cached.orders
	}
	if cfg.Overfin.URL == "" {
		return nil
	}
	orders, err := driver.inv.OverfinClient().WithContext(ctx).EpisodeOrders(tmdbID)
	if err != nil {
		log.Printf("[adding] what Overfin says of series %d: %v", tmdbID, err)
		return nil
	}
	if driver.ordersCache == nil {
		driver.ordersCache = map[int]cachedOrders{}
	}
	driver.ordersCache[tmdbID] = cachedOrders{orders: orders, at: now}
	return orders
}

// tmdbOf is a series' TMDB ID, 0 when Sonarr has none or can't say.
func (driver *Driver) tmdbOf(client *sonarr.Client, seriesID int) int {
	titles, err := client.Titles()
	if err != nil {
		return 0
	}
	for _, title := range titles {
		if title.ID == seriesID {
			return title.TMDBID
		}
	}
	return 0
}

// importFor finds the record of a whole-season pack sent: by its download,
// or, the queue never having shown it, by its name.
func (state *State) importFor(pack *SeasonPack) *downloadImport {
	if pack.DownloadID != "" {
		return state.Imports[pack.DownloadID]
	}
	for id, download := range state.Imports {
		if download.SeriesID == pack.SeriesID && download.Title == pack.Release && !download.GrabbedAt.Before(pack.GrabbedAt.Add(-time.Minute)) {
			pack.DownloadID = id
			return download
		}
	}
	return nil
}

// followImported follows a pack sent when Overtide does the imports: until
// its files have all gone in the season is waiting for it; if some can't be
// told apart, or it failed, or it never came, another is tried later.
func (driver *Driver) followImported(client *sonarr.Client, state *State, pack *SeasonPack) {
	queue, err := client.Queue(pack.SeriesID)
	if err != nil {
		return // asked again next run
	}
	for _, item := range queue {
		if (pack.DownloadID != "" && item.DownloadID == pack.DownloadID) || (pack.DownloadID == "" && item.Title == pack.Release) {
			pack.DownloadID = item.DownloadID
			if item.TrackedDownloadState == "failed" || item.TrackedDownloadState == "failedPending" {
				driver.packFailed(pack)
			}
			return
		}
	}
	download := state.importFor(pack)
	switch {
	case download == nil:
		if driver.now().Sub(pack.GrabbedAt) >= importWait {
			driver.packFailed(pack)
		}
	case download.Failed || len(download.Waiting) > 0:
		if !slices.Contains(pack.Tried, pack.Release) {
			pack.Tried = append(pack.Tried, pack.Release)
		}
		driver.packFailed(pack)
	case download.Done:
		driver.packDone(state, pack)
	}
}

// importing reports whether a series has a download finished or going whose
// import isn't settled yet: it isn't to be taken for one that stopped.
func (state *State) importing(seriesID int) bool {
	if state == nil {
		return false
	}
	for _, download := range state.Imports {
		if download.SeriesID == seriesID && !download.Done && len(download.Waiting) == 0 {
			return true
		}
	}
	return false
}
