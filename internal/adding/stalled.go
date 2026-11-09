package adding

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// StallGiveUp is how long a download may go without progress before it's
// given up: long enough for a rare torrent whose seeders come and go.
const StallGiveUp = 48 * time.Hour

// stalledKey keeps each download's last progress, so a restart doesn't
// start the wait over.
const stalledKey = "adding.stalled"

// stallMark is a download's size left when it last moved, and since when.
type stallMark struct {
	Left  float64   `json:"left"`
	Since time.Time `json:"since"`
}

// queued is one download in a Radarr or Sonarr queue: its first queue
// item, for removing it, and what's left of it.
type queued struct {
	itemID int
	title  string
	status string
	left   float64
	size   float64
}

// giveUpStalled removes the downloads of Overtide's titles whose size left
// hasn't moved for StallGiveUp: from the queue and the download client,
// their data with them (an unfinished download: nothing was imported from
// it), their release blocklisted. Radarr and Sonarr then mark them failed,
// so following the downloads puts their title or season back in line for
// another release. A paused or queued download isn't stalled: it waits for
// the operator or a free slot.
func (service *Service) giveUpStalled(ctx context.Context, cfg config.Config, dryRun bool) {
	marks, err := service.stallMarks()
	if err != nil {
		log.Printf("[adding] stalled downloads: %v", err)
		return
	}
	ours := map[string]bool{}
	if items, _, err := service.inv.Snapshot(); err == nil {
		for _, m := range items {
			if service.managed(m) && !model.HasTag(m.Tags, model.TagIgnore) {
				ours[fmt.Sprintf("%s|%s|%d", m.ServiceID, m.Type, m.SourceID)] = true
			}
		}
	}
	now := service.now()
	seen := map[string]bool{}
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			continue
		}
		client = client.WithContext(ctx)
		queue, err := client.FullQueue()
		if err != nil {
			// Unread, its downloads keep their marks.
			markQueue(seen, marks, svc.ID)
			continue
		}
		downloads := map[string]*queued{}
		for _, item := range queue {
			if item.DownloadID == "" || !ours[fmt.Sprintf("%s|%s|%d", svc.ID, model.Movie, item.MovieID)] {
				continue
			}
			addQueued(downloads, item.DownloadID, item.ID, item.Title, item.Status, item.SizeLeft, item.Size)
		}
		service.judge(svc.ID, downloads, marks, seen, now, dryRun, client.GiveUpQueueItem)
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			continue
		}
		client = client.WithContext(ctx)
		queue, err := client.FullQueue()
		if err != nil {
			markQueue(seen, marks, svc.ID)
			continue
		}
		downloads := map[string]*queued{}
		for _, item := range queue {
			if item.DownloadID == "" || !ours[fmt.Sprintf("%s|%s|%d", svc.ID, model.Series, item.SeriesID)] {
				continue
			}
			addQueued(downloads, item.DownloadID, item.ID, item.Title, item.Status, item.SizeLeft, item.Size)
		}
		service.judge(svc.ID, downloads, marks, seen, now, dryRun, client.GiveUpQueueItem)
	}
	// Downloads gone from the queues are forgotten.
	for key := range marks {
		if !seen[key] {
			delete(marks, key)
		}
	}
	if err := service.saveStallMarks(marks); err != nil {
		log.Printf("[adding] stalled downloads: %v", err)
	}
}

// addQueued adds a queue item to its download. Sonarr lists a season
// pack once per episode, each with the whole download's size: the first
// is kept.
func addQueued(downloads map[string]*queued, downloadID string, itemID int, title, status string, left, size float64) {
	if downloads[downloadID] != nil {
		return
	}
	downloads[downloadID] = &queued{itemID: itemID, title: title, status: status, left: left, size: size}
}

// markQueue keeps the marks of a queue that couldn't be read.
func markQueue(seen map[string]bool, marks map[string]stallMark, serviceID string) {
	for key := range marks {
		if strings.HasPrefix(key, serviceID+"|") {
			seen[key] = true
		}
	}
}

// judge looks at one queue's downloads: one that moved gets a new mark, one
// unmoved for StallGiveUp is given up.
func (service *Service) judge(serviceID string, downloads map[string]*queued, marks map[string]stallMark, seen map[string]bool, now time.Time, dryRun bool, giveUp func(int) error) {
	for downloadID, d := range downloads {
		key := serviceID + "|" + downloadID
		seen[key] = true
		// Only a download under way can stall: paused, queued, finished or
		// failed ones wait for something else.
		if (d.status != "downloading" && d.status != "warning") || d.left <= 0 {
			delete(marks, key)
			continue
		}
		mark, known := marks[key]
		if !known || mark.Left != d.left {
			marks[key] = stallMark{Left: d.left, Since: now}
			continue
		}
		if now.Sub(mark.Since) < StallGiveUp || dryRun {
			continue
		}
		if err := giveUp(d.itemID); err != nil {
			log.Printf("[adding] giving up %s, stalled since %s: %v", d.title, mark.Since.Format(time.RFC3339), err)
			continue
		}
		log.Printf("[adding] gave up %s: no progress for %s; its release is blocklisted, another is searched", d.title, StallGiveUp)
		delete(marks, key)
	}
}

func (service *Service) stallMarks() (map[string]stallMark, error) {
	marks := map[string]stallMark{}
	database := service.inv.Store()
	if database == nil {
		return nil, fmt.Errorf("no database")
	}
	raw, err := database.Meta(stalledKey)
	if err != nil || raw == "" {
		return marks, err
	}
	if err := json.Unmarshal([]byte(raw), &marks); err != nil {
		return nil, fmt.Errorf("read the stalled downloads: %w", err)
	}
	return marks, nil
}

func (service *Service) saveStallMarks(marks map[string]stallMark) error {
	encoded, err := json.Marshal(marks)
	if err != nil {
		return err
	}
	return service.inv.Store().SetMeta(stalledKey, string(encoded))
}
