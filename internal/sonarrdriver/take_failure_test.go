package sonarrdriver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
)

// A failure while taking keeps what was read: a series already taken isn't
// read again, so its wanted episodes would be lost for good.
func TestTakeFailureKeepsWhatItRead(t *testing.T) {
	now := *arrfake.Day("2026-10-30")
	fake := arrfake.New(now)
	fake.Series[5] = map[string]any{"id": 5, "title": "Old", "monitored": true, "monitorNewItems": "all", "tags": []int{},
		"seasons": []any{}, "added": arrfake.ISO(now.AddDate(-1, 0, 0))}
	fake.Eps[1] = map[string]any{"id": 1, "seriesId": 5, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": arrfake.ISO(now.AddDate(0, 0, -60)), "hasFile": false, "monitored": true}
	failing := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing && r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tag") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	defer srv.Close()
	cfg := config.Config{Services: []config.Service{{ID: "s", Type: "sonarr", URL: srv.URL + "/sonarr", APIKey: "k"}}}
	cfg.Adding.Mode, cfg.Adding.Manage = config.AddingOn, config.ManageAll
	driver := newTestDriver(t, cfg, now)
	label := func(time.Time) (string, bool) { return "overtide-found", false }
	if _, err := driver.Take(context.Background(), cfg, map[string]bool{}, label, false); err == nil {
		t.Fatal("no failure")
	}
	states, err := driver.States()
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := states["s"].Wanted[1]; !kept {
		t.Errorf("the wanted episode is lost: %+v", states["s"])
	}
}
