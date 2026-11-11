package sonarrdriver

import (
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
)

// A season or an episode removed by hand is never in the state a run
// reads, but what's downloading; a season's episodes go with it.
func TestRemoveWanted(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	driver := newTestDriver(t, config.Config{}, now)
	state := &State{Seasons: map[string]*SeasonPack{
		seasonKey(7, 2): {SeriesID: 7, Season: 2},
		seasonKey(7, 3): {SeriesID: 7, Season: 3, GrabbedAt: now},
	}, Wanted: map[int]EpisodeWant{
		100: {SeriesID: 7, Season: 2, Episode: 1},
		101: {SeriesID: 7, Season: 4, Episode: 1},
		102: {SeriesID: 7, Season: 4, Episode: 2, Grabbed: true},
	}}
	if err := driver.SaveStates(map[string]*State{"s": state}); err != nil {
		t.Fatal(err)
	}
	if err := driver.Remove(func(r *Removed, at time.Time) {
		r.Seasons[SeasonRef("s", 7, 2)] = at
		r.Seasons[SeasonRef("s", 7, 3)] = at
		r.Episodes[EpisodeRef("s", 101)] = at
		r.Episodes[EpisodeRef("s", 102)] = at
	}); err != nil {
		t.Fatal(err)
	}
	states, err := driver.States()
	if err != nil {
		t.Fatal(err)
	}
	got := states["s"]
	if _, ok := got.Seasons[seasonKey(7, 2)]; ok || got.Seasons[seasonKey(7, 3)] == nil {
		t.Fatalf("seasons: %+v", got.Seasons)
	}
	if _, ok := got.Wanted[100]; ok {
		t.Fatal("the removed season's episode stays")
	}
	if _, ok := got.Wanted[101]; ok {
		t.Fatal("the removed episode stays")
	}
	if _, ok := got.Wanted[102]; !ok {
		t.Fatal("a downloading episode went")
	}
}
