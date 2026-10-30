// Package trackers is Overtide's proxy between Radarr or Sonarr and
// Prowlarr: Prowlarr's apps reach their trackers through it once Prowlarr
// gives them its address, and it paces them tracker by tracker. A search
// for a tracker whose budget is spent gets "nothing found" instead of
// reaching it; RSS polls and grabs always go through.
package trackers

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limit is a tracker's budget, from Prowlarr: queries and grabs over Unit
// (0: none).
type Limit struct {
	Name    string
	Queries int
	Grabs   int
	Unit    time.Duration
}

// rssShare is the part of a tracker's query budget kept for RSS polls:
// searches stop short of it.
const rssShare = 0.2

// pause is how long a tracker rests after a 429 that didn't say.
const pause = time.Hour

// Request kinds, counted per tracker.
const (
	Search  = "search"
	RSS     = "rss"
	Grab    = "grab"
	Refused = "refused" // a search answered "nothing found" by Overtide
	Limited = "limited" // a 429 from Prowlarr or the tracker
)

type event struct {
	at   time.Time
	kind string
}

// Gate is the proxy. Target gives Prowlarr's address (empty: no proxy);
// Public, the proxy's own address as Radarr and Sonarr reach it, which
// replaces Prowlarr's in the links its answers carry.
type Gate struct {
	Target func() string
	Public func() string
	// Limits gives the trackers' budgets, read again after limitsEvery.
	Limits func() (map[int]Limit, error)

	mu       sync.Mutex
	now      func() time.Time
	events   map[int][]event
	limits   map[int]Limit
	readAt   time.Time
	resting  map[int]time.Time
	upstream *http.Client
}

const limitsEvery = time.Hour

func NewGate(target, public func() string, limits func() (map[int]Limit, error)) *Gate {
	return &Gate{Target: target, Public: public, Limits: limits, now: time.Now,
		events: map[int][]event{}, resting: map[int]time.Time{},
		// Prowlarr's redirects (a magnet link) go back to Radarr or Sonarr
		// as they are.
		upstream: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// emptyFeed is a Torznab answer with no release.
const emptyFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel><title>Overtide</title></channel></rss>
`

// kindOf says what a Torznab request is: a search when it asks for
// something, an RSS poll otherwise; "" for what isn't counted (caps).
func kindOf(query url.Values) string {
	if query.Get("t") == "caps" {
		return ""
	}
	for _, key := range []string{"q", "imdbid", "tmdbid", "tvdbid", "tvmazeid", "rid", "season", "ep"} {
		if query.Get(key) != "" {
			return Search
		}
	}
	return RSS
}

func (gate *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimRight(gate.Target(), "/")
	if target == "" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	idText, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/prowlarr/"), "/")
	id, err := strconv.Atoi(idText)
	if err != nil || (rest != "api" && rest != "download") {
		http.NotFound(w, r)
		return
	}
	kind := Grab
	if rest == "api" {
		kind = kindOf(r.URL.Query())
	}
	if kind == Search && !gate.allow(id) {
		gate.record(id, Refused)
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		_, _ = io.WriteString(w, emptyFeed)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target+"/"+strconv.Itoa(id)+"/"+rest+"?"+r.URL.RawQuery, nil)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	response, err := gate.upstream.Do(request)
	if err != nil {
		http.Error(w, "Prowlarr unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if kind != "" {
		gate.record(id, kind)
	}
	if response.StatusCode == http.StatusTooManyRequests {
		gate.rest(id, response.Header.Get("Retry-After"))
		if rest == "api" {
			// "Nothing found", not an error: Radarr and Sonarr would mark
			// the tracker as failing and leave it for longer.
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
			_, _ = io.WriteString(w, emptyFeed)
			return
		}
	}
	for key, values := range response.Header {
		if key == "Content-Length" {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if rest != "api" {
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
		return
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		http.Error(w, "Prowlarr's answer was cut: "+err.Error(), http.StatusBadGateway)
		return
	}
	// Prowlarr writes its links with its own address: they come back
	// through here, so grabs are counted too.
	if public := strings.TrimRight(gate.Public(), "/"); public != "" {
		body = bytes.ReplaceAll(body, []byte(target+"/"), []byte(public+"/"))
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

// allow reports whether a search may reach the tracker: not resting after
// a 429, and short of its query budget less the RSS share.
func (gate *Gate) allow(id int) bool {
	gate.readLimits()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.now()
	if now.Before(gate.resting[id]) {
		return false
	}
	limit := gate.limits[id]
	if limit.Queries <= 0 {
		return true
	}
	queries := 0
	for _, e := range gate.events[id] {
		if (e.kind == Search || e.kind == RSS) && now.Sub(e.at) < limit.Unit {
			queries++
		}
	}
	return float64(queries) < float64(limit.Queries)*(1-rssShare)
}

func (gate *Gate) readLimits() {
	gate.mu.Lock()
	stale := gate.Limits != nil && gate.now().Sub(gate.readAt) >= limitsEvery
	if stale {
		gate.readAt = gate.now() // one reader at a time
	}
	gate.mu.Unlock()
	if !stale {
		return
	}
	limits, err := gate.Limits()
	if err != nil {
		return
	}
	gate.mu.Lock()
	gate.limits = limits
	gate.mu.Unlock()
}

func (gate *Gate) rest(id int, retryAfter string) {
	wait := pause
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && seconds > 0 {
		wait = time.Duration(seconds) * time.Second
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.resting[id] = gate.now().Add(wait)
	gate.events[id] = append(gate.events[id], event{gate.now(), Limited})
}

// record counts one request, and forgets those older than a day.
func (gate *Gate) record(id int, kind string) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.now()
	kept := gate.events[id][:0]
	for _, e := range gate.events[id] {
		if now.Sub(e.at) < 24*time.Hour {
			kept = append(kept, e)
		}
	}
	gate.events[id] = append(kept, event{now, kind})
}

// Stat is what a tracker was asked over the last 24 hours.
type Stat struct {
	ID       int
	Name     string
	Searches int
	RSS      int
	Grabs    int
	Refused  int
	Limited  int
	// Limit is its query budget per LimitUnit (0: none known).
	Limit     int
	LimitUnit time.Duration
	// RestingUntil: after a 429, no search reaches it before then.
	RestingUntil time.Time
}

// Stats returns, per tracker, what it was asked over the last 24 hours.
func (gate *Gate) Stats() []Stat {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	now := gate.now()
	ids := map[int]bool{}
	for id := range gate.events {
		ids[id] = true
	}
	for id := range gate.limits {
		ids[id] = true
	}
	var stats []Stat
	for id := range ids {
		limit := gate.limits[id]
		stat := Stat{ID: id, Name: limit.Name, Limit: limit.Queries, LimitUnit: limit.Unit}
		if now.Before(gate.resting[id]) {
			stat.RestingUntil = gate.resting[id]
		}
		for _, e := range gate.events[id] {
			if now.Sub(e.at) >= 24*time.Hour {
				continue
			}
			switch e.kind {
			case Search:
				stat.Searches++
			case RSS:
				stat.RSS++
			case Grab:
				stat.Grabs++
			case Refused:
				stat.Refused++
			case Limited:
				stat.Limited++
			}
		}
		if stat.Name == "" {
			stat.Name = "Tracker " + strconv.Itoa(id)
		}
		stats = append(stats, stat)
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].Name < stats[j].Name })
	return stats
}
