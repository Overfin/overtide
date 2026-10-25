package adding

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/store"
)

// Overfin's picks and its trending and popular titles join the waiting
// list: films to the first Radarr, series (with a TheTVDB ID) to the first
// Sonarr; a title in both lists comes once; an import list's title Overfin
// suggests too becomes Overfin's; what Overfin stops suggesting leaves,
// and with no discovery share, discovery does.
func TestReadOverfin(t *testing.T) {
	answers := map[string]string{
		"":         `{"titles":[{"kind":"movie","name":"Pick","tmdbId":"1","score":80},{"kind":"season","name":"Show","tmdbId":"2","tvdbId":"22","season":1,"score":70},{"kind":"season","name":"Show","tmdbId":"2","tvdbId":"22","season":2,"score":75},{"kind":"season","name":"No TVDB","tmdbId":"3","season":1,"score":60}]}`,
		"trending": `{"titles":[{"kind":"movie","name":"Hot","tmdbId":"4","score":20},{"kind":"movie","name":"Pick","tmdbId":"1","score":80}]}`,
		"popular":  `{"titles":[{"kind":"movie","name":"Hot","tmdbId":"4","score":20},{"kind":"movie","name":"Known","tmdbId":"5","score":30}]}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("inLibrary") != "false" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(answers[r.URL.Query().Get("list")]))
	}))
	defer srv.Close()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	cfg := config.Config{Services: []config.Service{{ID: "r", Type: "radarr"}, {ID: "r2", Type: "radarr"}, {ID: "s", Type: "sonarr"}}}
	cfg.Overfin = config.Connection{URL: srv.URL, APIKey: "ofapp_k"}
	service := New(inventory.New(cfg, database))
	service.now = func() time.Time { return *day("2026-10-25") }

	entries := []Entry{{Key: movieKey(5), ServiceID: "r", Source: FromList, From: "A list"}}
	entries = service.readOverfin(context.Background(), cfg, entries)
	got := map[string]Entry{}
	for _, entry := range entries {
		got[entry.ServiceID+"|"+entry.Key] = entry
	}
	if len(entries) != 4 {
		t.Fatalf("%d entries: %+v", len(entries), entries)
	}
	if e := got["r|movie:1"]; e.Source != FromOverfin || e.Title != "Pick" || e.State != Waiting {
		t.Errorf("the pick: %+v", e)
	}
	if e := got["s|series:22"]; e.Source != FromOverfin || e.ExternalID != 22 || e.Position != 1 {
		t.Errorf("the series: %+v", e)
	}
	if e := got["r|movie:4"]; e.Source != FromDiscovery || e.From != "Trending" {
		t.Errorf("the trending film: %+v", e)
	}
	if e := got["r|movie:5"]; e.Source != FromDiscovery || e.From != "Popular" {
		t.Errorf("the list's film, popular too: %+v", e)
	}

	// No discovery share: discovery leaves, picks stay.
	zero := 0
	cfg.Adding.DiscoveryShare = &zero
	entries = service.readOverfin(context.Background(), cfg, entries)
	if len(entries) != 2 {
		t.Errorf("without discovery: %+v", entries)
	}
}

// Of Overfin's suggestions, the share comes from discovery, in turn with
// the picks; either fills in when the other runs out.
func TestInterleave(t *testing.T) {
	entries := []Entry{
		{Key: "p0", Source: FromOverfin, Position: 0},
		{Key: "p1", Source: FromOverfin, Position: 1},
		{Key: "p2", Source: FromOverfin, Position: 2},
		{Key: "pop0", Source: FromDiscovery, From: "Popular", Position: 0},
		{Key: "tr0", Source: FromDiscovery, From: "Trending", Position: 0},
		{Key: "list", Source: FromList},
		{Key: "request", Source: FromRequest},
	}
	order := func(share int) string {
		e := append([]Entry(nil), entries...)
		sortForTries(e, share)
		s := ""
		for _, entry := range e {
			s += entry.Key + " "
		}
		return s
	}
	if got := order(50); got != "request tr0 p0 pop0 p1 p2 list " {
		t.Errorf("half: %s", got)
	}
	if got := order(0); got != "request p0 p1 p2 tr0 pop0 list " {
		t.Errorf("none: %s", got)
	}
	if got := order(100); got != "request tr0 pop0 p0 p1 p2 list " {
		t.Errorf("all: %s", got)
	}
}
