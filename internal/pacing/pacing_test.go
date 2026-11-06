package pacing

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/services/prowlarr"
)

// fakeProwlarr has two trackers: A with 100 queries a day, B without a
// limit; used and failed are what its stats say.
type fakeProwlarr struct {
	usedA, failedB int
}

func (f *fakeProwlarr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/indexer":
		fmt.Fprint(w, `[{"id":14,"name":"TrackerA","fields":[{"name":"baseSettings.queryLimit","value":100},{"name":"baseSettings.limitsUnit","value":0}]},{"id":22,"name":"TrackerB","fields":[]}]`)
	case "/api/v1/indexerstats":
		if r.URL.Query().Get("startDate") == "" {
			http.Error(w, "no start", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `{"indexers":[{"indexerId":14,"numberOfQueries":%d,"numberOfRssQueries":10},{"indexerId":22,"numberOfQueries":500,"numberOfFailedQueries":%d}]}`, f.usedA-10, f.failedB)
	default:
		http.NotFound(w, r)
	}
}

func newPacer(t *testing.T, fake *fakeProwlarr) *Pacer {
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	client := prowlarr.New(srv.URL, "k")
	return New(func() *prowlarr.Client { return client })
}

// Searches go while A, the only one with a limit, has room short of the
// RSS share: up to 80 of its 100 queries a day.
func TestReady(t *testing.T) {
	fake := &fakeProwlarr{usedA: 79}
	pacer := newPacer(t, fake)
	if ok, why := pacer.Ready(); !ok {
		t.Fatalf("79 of 100 used: not ready, %q", why)
	}
	fake.usedA = 80
	ok, why := pacer.Ready()
	if ok || why != "TrackerA used 80 of its 100 queries today" {
		t.Fatalf("80 of 100 used: %v %q", ok, why)
	}
	trackers, err := pacer.Trackers()
	if err != nil || len(trackers) != 2 || trackers[0].Name != "TrackerA" || trackers[0].Searches != 0 || trackers[1].Limit != 0 {
		t.Fatalf("trackers: %+v %v", trackers, err)
	}
}

// A search that found nothing means nothing when a tracker failed, or one
// is at its limit; without Prowlarr, nothing is paced.
func TestUnavailable(t *testing.T) {
	fake := &fakeProwlarr{usedA: 50}
	pacer := newPacer(t, fake)
	since := time.Now().Add(-time.Minute)
	if down, why := pacer.Unavailable(since); down {
		t.Fatalf("all answered: %q", why)
	}
	fake.failedB = 1
	if down, why := pacer.Unavailable(since); !down || why != "TrackerB unavailable" {
		t.Fatalf("B failed: %v %q", down, why)
	}
	fake.failedB, fake.usedA = 0, 100
	if down, why := pacer.Unavailable(since); !down || why != "TrackerA unavailable" {
		t.Fatalf("A at its limit: %v %q", down, why)
	}
	none := New(func() *prowlarr.Client { return nil })
	if ok, _ := none.Ready(); !ok {
		t.Fatal("without Prowlarr, not ready")
	}
	if down, _ := none.Unavailable(since); down {
		t.Fatal("without Prowlarr, unavailable")
	}
}
