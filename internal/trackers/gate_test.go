package trackers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProwlarr answers Torznab for trackers 14 and 22, with links written
// with its own address; tracker 9 answers 429.
type fakeProwlarr struct {
	mu    sync.Mutex
	base  string
	asked []string
}

func (f *fakeProwlarr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.asked = append(f.asked, r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	switch {
	case strings.HasPrefix(r.URL.Path, "/5/"):
		http.Error(w, "tracker down", http.StatusInternalServerError)
	case strings.HasPrefix(r.URL.Path, "/9/"):
		w.Header().Set("Retry-After", "600")
		http.Error(w, "limit", http.StatusTooManyRequests)
	case strings.HasSuffix(r.URL.Path, "/download"):
		http.Redirect(w, r, "magnet:?xt=urn:btih:abc", http.StatusMovedPermanently)
	default:
		io.WriteString(w, `<rss><item><link>`+f.base+`/14/download?apikey=k&amp;link=x</link></item></rss>`)
	}
}

func (f *fakeProwlarr) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

func newTestGate(t *testing.T, limits map[int]Limit) (*Gate, *fakeProwlarr, *time.Time) {
	t.Helper()
	fake := &fakeProwlarr{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	fake.base = srv.URL
	gate := NewGate(func() string { return srv.URL }, func() string { return "http://overtide:8089/prowlarr" },
		func() (map[int]Limit, error) { return limits, nil })
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	gate.now = func() time.Time { return now }
	return gate, fake, &now
}

func get(gate *Gate, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestGatePassesAndCounts(t *testing.T) {
	gate, fake, _ := newTestGate(t, map[int]Limit{14: {Name: "TrackerA"}})
	w := get(gate, "/prowlarr/14/api?t=tvsearch&tvdbid=1&season=2&apikey=k")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "http://overtide:8089/prowlarr/14/download?apikey=k") {
		t.Fatalf("search: %d %s", w.Code, w.Body)
	}
	get(gate, "/prowlarr/14/api?t=search&cat=5000&apikey=k")
	get(gate, "/prowlarr/14/api?t=caps&apikey=k")
	w = get(gate, "/prowlarr/14/download?apikey=k&link=x")
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "magnet:?xt=urn:btih:abc" {
		t.Fatalf("grab: %d %v", w.Code, w.Header())
	}
	if fake.count() != 4 {
		t.Fatalf("Prowlarr asked %d times", fake.count())
	}
	stats := gate.Stats()
	if len(stats) != 1 || stats[0].Name != "TrackerA" || stats[0].Searches != 1 || stats[0].RSS != 1 || stats[0].Grabs != 1 {
		t.Fatalf("stats: %+v", stats)
	}
	if w := get(gate, "/prowlarr/14/other"); w.Code != http.StatusNotFound {
		t.Fatalf("other path: %d", w.Code)
	}
}

// With a budget of 10 queries a day, searches stop at 8 (2 kept for RSS):
// the 9th gets "nothing found" without reaching the tracker, RSS still
// goes through, and another tracker keeps answering.
func TestGateBudget(t *testing.T) {
	gate, fake, now := newTestGate(t, map[int]Limit{14: {Name: "TrackerA", Queries: 10, Unit: 24 * time.Hour}})
	for range 8 {
		get(gate, "/prowlarr/14/api?t=search&q=x&apikey=k")
	}
	before := fake.count()
	w := get(gate, "/prowlarr/14/api?t=search&q=x&apikey=k")
	if fake.count() != before || !strings.Contains(w.Body.String(), "<channel><title>Overtide</title></channel>") {
		t.Fatalf("9th search: reached the tracker, or %s", w.Body)
	}
	get(gate, "/prowlarr/14/api?t=search&cat=2000&apikey=k")
	get(gate, "/prowlarr/22/api?t=search&q=x&apikey=k")
	if fake.count() != before+2 {
		t.Fatalf("RSS or the other tracker held back: %d", fake.count()-before)
	}
	for _, stat := range gate.Stats() {
		if stat.ID == 14 && stat.Refused != 1 {
			t.Fatalf("stats: %+v", stat)
		}
	}
	// A day later, the budget is back.
	*now = now.Add(24 * time.Hour)
	before = fake.count()
	get(gate, "/prowlarr/14/api?t=search&q=x&apikey=k")
	if fake.count() != before+1 {
		t.Fatalf("the next day's search didn't go through")
	}
}

// A 429 is answered "nothing found", and the tracker rests for its
// Retry-After: searches don't reach it, RSS still does.
func TestGateRestsAfterA429(t *testing.T) {
	gate, fake, now := newTestGate(t, map[int]Limit{})
	w := get(gate, "/prowlarr/9/api?t=search&q=x&apikey=k")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<channel>") {
		t.Fatalf("429 passed as %d %s", w.Code, w.Body)
	}
	before := fake.count()
	get(gate, "/prowlarr/9/api?t=search&q=x&apikey=k")
	if fake.count() != before {
		t.Fatalf("a search reached the resting tracker")
	}
	get(gate, "/prowlarr/9/api?t=search&cat=2000&apikey=k")
	if fake.count() != before+1 {
		t.Fatalf("RSS held back")
	}
	*now = now.Add(11 * time.Minute)
	get(gate, "/prowlarr/9/api?t=search&q=x&apikey=k")
	if fake.count() != before+2 {
		t.Fatalf("still resting after its Retry-After")
	}
	if stats := gate.Stats(); stats[0].Limited < 1 || stats[0].Refused != 1 {
		t.Fatalf("stats: %+v", stats)
	}
}

// Whether the searches since a moment reached the trackers: all answered;
// one refused, failed or held back; none at all; Prowlarr unreachable.
func TestGateUnavailable(t *testing.T) {
	gate, _, now := newTestGate(t, map[int]Limit{14: {Name: "TrackerA"}, 22: {Name: "TrackerB"}, 9: {Name: "Busy"}, 5: {Name: "Down"}})
	since := *now
	if down, why := gate.Unavailable(since); !down || why != "no tracker answered" {
		t.Fatalf("no search yet: %v %q", down, why)
	}
	get(gate, "/prowlarr/14/api?t=search&q=x&apikey=k")
	get(gate, "/prowlarr/22/api?t=search&q=x&apikey=k")
	get(gate, "/prowlarr/14/api?t=search&cat=2000&apikey=k") // RSS: not a search
	if down, why := gate.Unavailable(since); down {
		t.Fatalf("all answered: %q", why)
	}
	get(gate, "/prowlarr/9/api?t=search&q=x&apikey=k")
	get(gate, "/prowlarr/5/api?t=search&q=x&apikey=k")
	if down, why := gate.Unavailable(since); !down || why != "Busy, Down unavailable" {
		t.Fatalf("one refused, one failed: %v %q", down, why)
	}
	// Later searches, all answering, start a new window.
	*now = now.Add(time.Minute)
	since = *now
	get(gate, "/prowlarr/14/api?t=search&q=x&apikey=k")
	if down, why := gate.Unavailable(since); down {
		t.Fatalf("new window: %q", why)
	}
	// Prowlarr unreachable.
	gone := NewGate(func() string { return "http://127.0.0.1:1" }, func() string { return "" }, nil)
	gone.now = gate.now
	get(gone, "/prowlarr/14/api?t=search&q=x&apikey=k")
	if down, _ := gone.Unavailable(since); !down {
		t.Fatalf("Prowlarr unreachable counted as answered")
	}
}

// Prowlarr's test of an app (indexer 0) isn't a tracker; names come from
// Prowlarr before any search.
func TestGateNamesAndPlaceholder(t *testing.T) {
	gate, fake, _ := newTestGate(t, map[int]Limit{14: {Name: "TrackerA"}, 22: {Name: "TrackerB"}})
	get(gate, "/prowlarr/0/api?t=search&apikey=k")
	if fake.count() != 1 {
		t.Fatalf("Prowlarr's test not passed on")
	}
	stats := gate.Stats()
	if len(stats) != 2 || stats[0].Name != "TrackerA" || stats[1].Name != "TrackerB" {
		t.Fatalf("stats: %+v", stats)
	}
}
