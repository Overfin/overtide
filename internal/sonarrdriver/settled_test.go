package sonarrdriver

import (
	"context"
	"testing"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
)

// A season that needed a look leaves the list once every aired episode
// has its file; one still missing an episode stays.
func TestSettleLooks(t *testing.T) {
	now := *arrfake.Day("2026-11-15")
	fake := arrfake.New(now)
	old := now.AddDate(-1, 0, 0)
	fake.AddSeries(5, nil, arrfake.Season(1, 2, 2), arrfake.Season(2, 2, 1))
	fake.AddEpisode(51, 5, 1, old, true)
	fake.AddEpisode(52, 5, 1, old, true) // complete since
	fake.AddEpisode(53, 5, 2, old, true)
	fake.AddEpisode(54, 5, 2, old, false) // still missing
	driver, cfg := testDriver(t, fake, config.ManageAll)
	if err := driver.inv.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := driver.SaveStates(map[string]*State{"s": {Seasons: map[string]*SeasonPack{
		seasonKey(5, 1): {SeriesID: 5, Season: 1, Short: 1, Done: true},
		seasonKey(5, 2): {SeriesID: 5, Season: 2, Unmatched: []string{"Other.S02E01.mkv"}},
	}}}); err != nil {
		t.Fatal(err)
	}
	driver.SettleLooks(context.Background(), cfg)
	states, _ := driver.States()
	if p := states["s"].Seasons[seasonKey(5, 1)]; p.short() || !p.Done {
		t.Errorf("complete season still needs a look: %+v", p)
	}
	if p := states["s"].Seasons[seasonKey(5, 2)]; !p.short() {
		t.Errorf("season still missing an episode settled: %+v", p)
	}
	_, seasons, _ := driver.Wanted()
	if len(seasons) != 1 || seasons[0].Why != "Needs a look" {
		t.Errorf("rows: %+v", seasons)
	}
}
