package adding

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/overfin/overtide/internal/model"
)

// Live is what Overtide is doing this moment, for the Adding page.
type Live struct {
	// Running: a run is on; Phase says which part of it.
	Running bool
	Phase   string
	// Title is the title being tried, and Step what's being done with it,
	// since StepSince.
	Title     string
	Step      string
	StepSince time.Time
	// Searches are the run's so far, of at most Budget.
	Searches, Budget int
	// DryRun: what it does, it would only do.
	DryRun bool
	// Stopped is why the run's searches stopped early, if they did.
	Stopped string
	// DailyUntil, when set, is when the titles a day let Overfin's
	// suggestions start again: the last run held them back.
	DailyUntil time.Time
}

// Live returns what Overtide is doing now.
func (service *Service) Live() Live {
	service.liveMu.Lock()
	defer service.liveMu.Unlock()
	return service.live
}

func (service *Service) setLive(change func(*Live)) {
	service.liveMu.Lock()
	defer service.liveMu.Unlock()
	change(&service.live)
}

// phase starts a part of the run.
func (service *Service) phase(name string) {
	service.setLive(func(l *Live) { l.Phase, l.Title, l.Step = name, "", "" })
}

// step says what's done with a title now.
func (service *Service) step(title, what string) {
	now := service.now()
	service.setLive(func(l *Live) { l.Title, l.Step, l.StepSince = title, what, now })
}

// Download is a film or a season in Radarr's or Sonarr's queue, or a title
// Overtide started that left it.
type Download struct {
	Title string
	Kind  model.MediaType
	// Season and Episodes: a series' season, and how many of its episodes
	// are in the queue.
	Season   int
	Episodes int
	// Progress is from 0 to 1; Done is when the download client thinks
	// it's done (zero when it doesn't say); Status is the download's or
	// its import's.
	Progress float64
	Done     time.Time
	Status   string
	// Queued is false once it left the queue (imported, or stopped):
	// the next run says which.
	Queued bool
}

// Downloads lists what's downloading among the titles Overtide manages,
// from Radarr's and Sonarr's queues, whatever started it (a search, a pack,
// the RSS): a film, or a series' season adding up its episodes. A title it
// started that isn't there any more is listed too, until the next run says
// what became of it.
func (service *Service) Downloads(ctx context.Context) ([]Download, error) {
	entries, err := service.Entries()
	if err != nil {
		return nil, err
	}
	items, _, _ := service.inv.Snapshot()
	known := map[string]model.Media{}
	for _, m := range items {
		if service.managed(m) && !model.HasTag(m.Tags, model.TagIgnore) {
			known[fmt.Sprintf("%s|%s|%d", m.ServiceID, m.Type, m.SourceID)] = m
		}
	}
	type progress struct {
		download Download
		size     float64
		left     float64
	}
	groups := map[string]*progress{}
	queuedTitles := map[string]bool{}
	add := func(key string, d Download, size, left float64, done *time.Time, status, state string) {
		p := groups[key]
		if p == nil {
			p = &progress{download: d}
			p.download.Queued = true
			groups[key] = p
		}
		p.download.Episodes++
		p.size, p.left = p.size+size, p.left+left
		if done != nil && done.After(p.download.Done) {
			p.download.Done = *done
		}
		if p.download.Status == "" || state == "importPending" || state == "importBlocked" || state == "failedPending" {
			p.download.Status = statusOf(status, state)
		}
	}
	cfg := service.inv.Config()
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			continue
		}
		queue, _ := client.WithContext(ctx).FullQueue()
		for _, item := range queue {
			title := fmt.Sprintf("%s|%s|%d", svc.ID, model.Series, item.SeriesID)
			m, ok := known[title]
			if !ok {
				continue
			}
			queuedTitles[title] = true
			key := fmt.Sprintf("%s/%d", title, item.SeasonNumber)
			add(key, Download{Title: m.Title, Kind: model.Series, Season: item.SeasonNumber}, item.Size, item.SizeLeft, item.EstimatedCompletion, item.Status, item.TrackedDownloadState)
		}
	}
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			continue
		}
		queue, _ := client.WithContext(ctx).FullQueue()
		for _, item := range queue {
			title := fmt.Sprintf("%s|%s|%d", svc.ID, model.Movie, item.MovieID)
			m, ok := known[title]
			if !ok {
				continue
			}
			queuedTitles[title] = true
			add(title, Download{Title: m.Title, Kind: model.Movie}, item.Size, item.SizeLeft, item.EstimatedCompletion, item.Status, item.TrackedDownloadState)
		}
	}
	var out []Download
	for _, p := range groups {
		if p.size > 0 {
			p.download.Progress = 1 - p.left/p.size
		}
		out = append(out, p.download)
	}
	for _, entry := range entries {
		if entry.State == Downloading && entry.ArrID != 0 && !queuedTitles[fmt.Sprintf("%s|%s|%d", entry.ServiceID, entry.Kind, entry.ArrID)] {
			out = append(out, Download{Title: entry.Title, Kind: entry.Kind})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Progress != out[j].Progress {
			return out[i].Progress > out[j].Progress
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Season < out[j].Season
	})
	return out, nil
}

// statusOf says a download's state in words.
func statusOf(status, state string) string {
	switch state {
	case "importPending":
		return "Waiting to be imported"
	case "importing":
		return "Being imported"
	case "importBlocked":
		return "Import blocked: needs a look in Radarr or Sonarr"
	case "failedPending", "failed":
		return "Failed"
	}
	switch status {
	case "queued", "delay":
		return "Queued"
	case "paused":
		return "Paused"
	case "completed":
		return "Downloaded"
	case "warning":
		return "Stalled"
	}
	return "Downloading"
}

// Next lists the next titles to be tried, in order, at most n.
func (service *Service) Next(n int) ([]Entry, error) {
	entries, err := service.Entries()
	if err != nil {
		return nil, err
	}
	var next []Entry
	for _, entry := range entries {
		if entry.State == Waiting {
			next = append(next, entry)
		}
	}
	// Due ones first, in the order tried; then by their next try.
	now := service.now()
	sort.SliceStable(next, func(i, j int) bool {
		di, dj := !next[i].NextTry.After(now), !next[j].NextTry.After(now)
		if di != dj {
			return di
		}
		if !di {
			return next[i].NextTry.Before(next[j].NextTry)
		}
		return false
	})
	if len(next) > n {
		next = next[:n]
	}
	return next, nil
}
