package sonarrdriver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/overfin"
)

// An import holds the files only while Sonarr imports: a removal waits for
// that import, not for the rest of the pass, and an import waits for a
// removal under way.
func TestImportHoldsTheFilesForItsStepOnly(t *testing.T) {
	now := *arrfake.Day("2026-11-01")
	fake, _, _ := importTestDriver(t, now)
	reached := make(chan struct{})
	finish := make(chan struct{})
	var once, finished sync.Once
	end := func() { finished.Do(func() { close(finish) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sonarr importing: the import command (2) runs until finish.
		if strings.HasSuffix(r.URL.Path, "/api/v3/command/2") {
			once.Do(func() { close(reached) })
			<-finish
		}
		fake.ServeHTTP(w, r)
	}))
	defer srv.Close()
	defer end() // before the server closes, whatever happens
	cfg := config.Config{Services: []config.Service{{ID: "s", Type: "sonarr", URL: srv.URL + "/sonarr", APIKey: "k"}}}
	cfg.Adding.Mode, cfg.Adding.Manage = config.AddingOn, config.ManageAll
	driver := newTestDriver(t, cfg, now)
	driver.torrentFiles = func(context.Context, string) ([]string, bool, error) {
		return []string{"Show.S01E01.mkv", "Show.S01E02.mkv"}, true, nil
	}
	driver.knowOrders(5, &overfin.EpisodeOrders{Seasons: []overfin.OrderSeason{{Season: 1, Episodes: []overfin.OrderEpisode{
		{Episode: 1, AirDate: "2025-01-01", Name: "Title 1"}, {Episode: 2, AirDate: "2025-01-01", Name: "Title 2"}}}}})
	fake.Grabs = []map[string]any{{"downloadId": "D", "seriesId": 1, "sourceTitle": "Show.S01", "date": arrfake.ISO(now.Add(-time.Hour))}}
	fake.Downloads["D"] = []map[string]any{file("/dl/Show.S01/Show.S01E01.mkv", 1, 101), file("/dl/Show.S01/Show.S01E02.mkv", 1, 102)}

	// A removal under way: the import waits for it.
	var unlocked sync.Once
	locked := driver.inv.LockFiles()
	release := func() { unlocked.Do(locked) }
	imported := make(chan struct{})
	go func() {
		driver.ImportDownloads(context.Background(), cfg, false)
		close(imported)
	}()
	// Whatever happens, the import ends before the test does.
	defer func() { release(); end(); <-imported }()
	time.Sleep(100 * time.Millisecond)
	fake.Mu.Lock()
	early := len(fake.Imported)
	fake.Mu.Unlock()
	if early != 0 {
		t.Fatal("imported while a removal held the files")
	}
	release()

	// Sonarr importing: a removal waits, and goes as soon as it's done.
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("never imported")
	}
	removal := make(chan func())
	go func() { removal <- driver.inv.LockFiles() }()
	select {
	case <-removal:
		t.Fatal("a removal went while Sonarr imported")
	case <-time.After(100 * time.Millisecond):
	}
	end()
	select {
	case release := <-removal:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("the removal still waits after the import")
	}
}
