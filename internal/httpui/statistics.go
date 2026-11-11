package httpui

import (
	"net/http"
	"time"

	"github.com/overfin/overtide/internal/store"
)

// The Statistics page: what came and what went over a range, by period,
// and each disk's use over time.

type statsPeriod struct {
	Start         string `json:"start"`
	Films         int64  `json:"films"`
	Episodes      int64  `json:"episodes"`
	FilmBytes     int64  `json:"filmBytes"`
	EpisodeBytes  int64  `json:"episodeBytes"`
	ByOvertide    int64  `json:"byOvertide"`
	OvertideBytes int64  `json:"overtideBytes"`
	Titles        int64  `json:"titlesRemoved"`
	Torrents      int64  `json:"torrentsRemoved"`
	Other         int64  `json:"otherRemoved"`
	FreedBytes    int64  `json:"freedBytes"`
}

type statsDisk struct {
	Device  string            `json:"device"`
	Samples []statsDiskSample `json:"samples"`
}

type statsDiskSample struct {
	Day      string `json:"day"`
	Used     int64  `json:"used"`
	Services int64  `json:"services"`
	Total    int64  `json:"total"`
}

type statsData struct {
	Range  string        `json:"range"`
	Period store.Period  `json:"period"`
	Since  string        `json:"since"`
	Rows   []statsPeriod `json:"periods"`
	Totals statsPeriod   `json:"totals"`
	Disks  []statsDisk   `json:"disks"`
	// SamplesSince is the first day disks were sampled ("" before any).
	SamplesSince string `json:"samplesSince,omitempty"`
}

// statsRanges are the ranges offered, with how each is grouped.
var statsRanges = map[string]struct {
	days   int
	period store.Period
}{
	"30d": {30, store.ByDay},
	"90d": {90, store.ByWeek},
	"1y":  {365, store.ByWeek},
	"all": {0, store.ByMonth},
}

// statistics answers GET /api/statistics?range=30d|90d|1y|all (90d when
// absent).
func (server *Server) statistics(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("range")
	if name == "" {
		name = "90d"
	}
	rng, ok := statsRanges[name]
	if !ok {
		fail(w, http.StatusBadRequest, "range is 30d, 90d, 1y or all")
		return
	}
	db := server.inv.Store()
	now := time.Now().UTC()
	since := store.Today(now.AddDate(0, 0, -rng.days+1))
	if rng.days == 0 {
		first, err := db.FirstStatisticsDay()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		if first == "" {
			first = store.Today(now)
		}
		since = first
	}
	imports, err := db.ImportsSince(since, rng.period)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	removals, err := db.RemovalsSince(since, rng.period)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	samples, err := db.DiskSamplesSince(since)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := statsData{Range: name, Period: rng.period, Since: since, Rows: []statsPeriod{}, Disks: []statsDisk{}}
	byStart := map[string]*statsPeriod{}
	for _, start := range periodStarts(since, store.Today(now), rng.period) {
		out.Rows = append(out.Rows, statsPeriod{Start: start})
	}
	for i := range out.Rows {
		byStart[out.Rows[i].Start] = &out.Rows[i]
	}
	for _, c := range imports {
		if p := byStart[c.Start]; p != nil {
			p.Films, p.Episodes, p.FilmBytes, p.EpisodeBytes, p.ByOvertide, p.OvertideBytes = c.Films, c.Episodes, c.FilmBytes, c.EpisodeBytes, c.ByOvertide, c.OvertideBytes
		}
	}
	for _, c := range removals {
		if p := byStart[c.Start]; p != nil {
			p.Titles, p.Torrents, p.Other, p.FreedBytes = c.Titles, c.Torrents, c.Other, c.FreedBytes
		}
	}
	t := &out.Totals
	for _, p := range out.Rows {
		t.Films, t.Episodes, t.FilmBytes, t.EpisodeBytes = t.Films+p.Films, t.Episodes+p.Episodes, t.FilmBytes+p.FilmBytes, t.EpisodeBytes+p.EpisodeBytes
		t.ByOvertide, t.OvertideBytes = t.ByOvertide+p.ByOvertide, t.OvertideBytes+p.OvertideBytes
		t.Titles, t.Torrents, t.Other, t.FreedBytes = t.Titles+p.Titles, t.Torrents+p.Torrents, t.Other+p.Other, t.FreedBytes+p.FreedBytes
	}
	index := map[string]int{}
	for _, s := range samples {
		i, ok := index[s.Device]
		if !ok {
			i = len(out.Disks)
			index[s.Device] = i
			out.Disks = append(out.Disks, statsDisk{Device: s.Device})
		}
		out.Disks[i].Samples = append(out.Disks[i].Samples, statsDiskSample{Day: s.Day, Used: s.UsedBytes, Services: s.ServicesBytes, Total: s.TotalBytes})
	}
	if first, err := db.DiskSamplesSince(""); err == nil && len(first) > 0 {
		out.SamplesSince = first[0].Day
	}
	writeJSON(w, http.StatusOK, out)
}

// periodStarts lists every period's first day from since to today.
func periodStarts(since, today string, period store.Period) []string {
	from, err := time.Parse(time.DateOnly, since)
	if err != nil {
		return nil
	}
	to, _ := time.Parse(time.DateOnly, today)
	switch period {
	case store.ByWeek:
		from = from.AddDate(0, 0, -((int(from.Weekday()) + 6) % 7))
	case store.ByMonth:
		from = time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	var out []string
	for d := from; !d.After(to); {
		out = append(out, d.Format(time.DateOnly))
		switch period {
		case store.ByWeek:
			d = d.AddDate(0, 0, 7)
		case store.ByMonth:
			d = d.AddDate(0, 1, 0)
		default:
			d = d.AddDate(0, 0, 1)
		}
	}
	return out
}
