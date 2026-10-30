package httpui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/trackers"
)

// The proxy answers without Overtide's sign-in (Prowlarr checks its own
// API key); without Prowlarr connected, it answers nothing.
func TestProxyIsOutsideTheSignIn(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	postForm(t, handler, "/setup", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}, "confirm": {"correct-horse-battery"}}, nil)
	response := getPath(t, handler, "/prowlarr/14/api?t=caps&apikey=k", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("without Prowlarr: %d, %v", response.Code, response.Header())
	}
	if response := getPath(t, handler, "/storage", nil); response.Code != http.StatusSeeOther {
		t.Fatalf("a page answered without a session: %d", response.Code)
	}
}

// The Services page shows what each tracker was asked through Overtide.
func TestServicesShowTrackers(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	data := servicesData{
		Prowlarr: config.ProwlarrConfig{Route: true, APIKey: "k"},
		Trackers: []trackers.Stat{
			{Name: "TrackerA", Searches: 8, RSS: 40, Grabs: 2, Refused: 3, Limited: 1, Limit: 50, LimitUnit: 24 * time.Hour, RestingUntil: time.Now().Add(time.Hour)},
			{Name: "TrackerB", Searches: 30, Limit: 10, LimitUnit: time.Hour},
		},
	}
	recorder := httptest.NewRecorder()
	if err := renderTemplate(recorder, server.servicesTpl, data); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	for _, want := range []string{"Searches through Overtide", "TrackerA", "50 a day", "resting until", "(1 refused by the tracker)", "TrackerB", "10 an hour"} {
		if !strings.Contains(body, want) {
			t.Fatalf("no %q in:\n%s", want, body)
		}
	}
}
