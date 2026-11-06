// Package pacing keeps Overtide's searches within the trackers' limits,
// as Prowlarr sets and counts them: a search goes only while every
// tracker with a query limit has room for it, keeping a share of each
// limit for Radarr's and Sonarr's RSS polls. Prowlarr counts every query,
// whoever asked, so the polls and the operator's own searches are in.
package pacing

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/services/prowlarr"
)

// rssShare is the part of a tracker's query limit kept for RSS polls:
// searches stop short of it.
const rssShare = 0.2

// limitsEvery is how often the limits are read again.
const limitsEvery = time.Hour

type Pacer struct {
	// Prowlarr gives the client of Prowlarr; nil, nothing is paced.
	Prowlarr func() *prowlarr.Client

	mu       sync.Mutex
	now      func() time.Time
	indexers []prowlarr.Indexer
	readAt   time.Time
	from     *prowlarr.Client
}

func New(client func() *prowlarr.Client) *Pacer {
	return &Pacer{Prowlarr: client, now: time.Now}
}

// Tracker is where one tracker stands: its query limit (0: none) over
// Unit, and what it was asked over that time.
type Tracker struct {
	ID    int
	Name  string
	Limit int
	Unit  time.Duration
	Used  int
	// Searches is how many more searches it has room for (Limit less the
	// RSS share, less Used); meaningless without a limit.
	Searches int
}

// Trackers returns where each tracker stands, by name.
func (pacer *Pacer) Trackers() ([]Tracker, error) {
	client := pacer.Prowlarr()
	if client == nil {
		return nil, nil
	}
	indexers, err := pacer.limits(client)
	if err != nil {
		return nil, err
	}
	now := pacer.now()
	usage := map[time.Duration]map[int]prowlarr.Usage{}
	var trackers []Tracker
	for _, indexer := range indexers {
		tracker := Tracker{ID: indexer.ID, Name: indexer.Name, Limit: indexer.QueryLimit, Unit: indexer.LimitsUnit}
		if usage[indexer.LimitsUnit] == nil {
			if usage[indexer.LimitsUnit], err = client.Usage(now.Add(-indexer.LimitsUnit)); err != nil {
				return nil, err
			}
		}
		tracker.Used = usage[indexer.LimitsUnit][indexer.ID].Queries
		tracker.Searches = int(float64(tracker.Limit)*(1-rssShare)) - tracker.Used
		trackers = append(trackers, tracker)
	}
	sort.Slice(trackers, func(i, j int) bool { return trackers[i].Name < trackers[j].Name })
	return trackers, nil
}

// Ready reports whether one more search may go: every tracker with a
// limit has room for it. If not, why says which.
func (pacer *Pacer) Ready() (bool, string) {
	trackers, err := pacer.Trackers()
	if err != nil {
		return false, "Prowlarr can't be read: " + err.Error()
	}
	var full []string
	for _, tracker := range trackers {
		if tracker.Limit > 0 && tracker.Searches <= 0 {
			full = append(full, fmt.Sprintf("%s used %d of its %d queries %s", tracker.Name, tracker.Used, tracker.Limit, per(tracker.Unit)))
		}
	}
	if len(full) > 0 {
		return false, strings.Join(full, ", ")
	}
	return true, ""
}

// Unavailable reports whether searches made since then missed some
// trackers: a query that failed, or a tracker at its limit (Prowlarr
// refuses its queries). A search that found nothing means nothing then.
func (pacer *Pacer) Unavailable(since time.Time) (bool, string) {
	client := pacer.Prowlarr()
	if client == nil {
		return false, ""
	}
	indexers, err := pacer.limits(client)
	if err != nil {
		return true, "Prowlarr can't be read"
	}
	usage, err := client.Usage(since)
	if err != nil {
		return true, "Prowlarr can't be read"
	}
	trackers, err := pacer.Trackers()
	if err != nil {
		return true, "Prowlarr can't be read"
	}
	atLimit := map[int]bool{}
	for _, tracker := range trackers {
		atLimit[tracker.ID] = tracker.Limit > 0 && tracker.Used >= tracker.Limit
	}
	var missed []string
	for _, indexer := range indexers {
		if usage[indexer.ID].Failed > 0 || atLimit[indexer.ID] {
			missed = append(missed, indexer.Name)
		}
	}
	if len(missed) > 0 {
		sort.Strings(missed)
		return true, strings.Join(missed, ", ") + " unavailable"
	}
	return false, ""
}

// limits returns the trackers and their limits, read again after
// limitsEvery or when Prowlarr changed.
func (pacer *Pacer) limits(client *prowlarr.Client) ([]prowlarr.Indexer, error) {
	pacer.mu.Lock()
	defer pacer.mu.Unlock()
	if pacer.from == client && pacer.now().Sub(pacer.readAt) < limitsEvery {
		return pacer.indexers, nil
	}
	indexers, err := client.Indexers()
	if err != nil {
		return nil, err
	}
	pacer.indexers, pacer.readAt, pacer.from = indexers, pacer.now(), client
	return indexers, nil
}

func per(unit time.Duration) string {
	if unit == time.Hour {
		return "this hour"
	}
	return "today"
}
