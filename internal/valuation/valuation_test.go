package valuation

import (
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
	"testing"
	"time"
)

// sufficientTorrentHistory gives every "client|hash" key in keys a single
// sample old enough to satisfy torrentHistorySufficient's elapsed-time
// path, so tests unrelated to that gate aren't tripped by it.
func testConfig() config.Config {
	var c config.Config
	c.Overfin.URL = "http://overfin.example.org"
	c.Kept = []config.KeptItem{{Key: config.KeptMediaKey("movie", "", 4)}, {Key: config.KeptTorrentKey("kept")}}
	return c
}

func sufficientTorrentHistory(keys ...string) map[string][]store.TorrentHistorySample {
	sample := store.TorrentHistorySample{SampledAt: time.Now().Add(-TorrentContributionWindow - time.Hour)}
	history := make(map[string][]store.TorrentHistorySample, len(keys))
	for _, key := range keys {
		history[key] = []store.TorrentHistorySample{sample}
	}
	return history
}

func TestApplyMediaSetsRemovalRestrictedFromServiceOptIn(t *testing.T) {
	c := testConfig()
	c.Services = []config.Service{
		{ID: "radarr-in", AllowAutomaticRemoval: true},
		{ID: "radarr-out", AllowAutomaticRemoval: false},
	}
	items := []model.Media{
		{Title: "Opted in", ServiceID: "radarr-in"},
		{Title: "Opted out", ServiceID: "radarr-out"},
		{Title: "Unknown service", ServiceID: "does-not-exist"},
		{Title: "Series", Type: model.Series, ServiceID: "radarr-out", Seasons: []model.Season{{Number: 1}}},
	}
	ApplyMedia(items, c)
	if items[0].RemovalRestricted {
		t.Fatalf("expected an opted-in service's media to not be removal-restricted: %#v", items[0])
	}
	if !items[1].RemovalRestricted {
		t.Fatalf("expected an opted-out service's media to be removal-restricted: %#v", items[1])
	}
	if !items[2].RemovalRestricted {
		t.Fatalf("expected an unrecognized service id to fail closed to removal-restricted: %#v", items[2])
	}
	if !items[3].RemovalRestricted || !items[3].Seasons[0].RemovalRestricted {
		t.Fatalf("expected a season to inherit its series' RemovalRestricted: %#v", items[3])
	}
}

func TestApplyTorrentValueSetsRemovalRestrictedFromServiceOptIn(t *testing.T) {
	c := testConfig()
	c.Services = []config.Service{{ID: "qbittorrent-in", AllowAutomaticRemoval: true}}
	torrents := []model.Torrent{
		{Hash: "in", ServiceID: "qbittorrent-in"},
		{Hash: "out", ServiceID: "does-not-exist"},
	}
	ApplyTorrentValue(torrents, c, nil)
	if torrents[0].RemovalRestricted {
		t.Fatalf("expected an opted-in service's torrent to not be removal-restricted: %#v", torrents[0])
	}
	if !torrents[1].RemovalRestricted {
		t.Fatalf("expected an unrecognized service id to fail closed to removal-restricted: %#v", torrents[1])
	}
}

func TestApplyMediaSeasonsInheritSeriesWideProtection(t *testing.T) {
	c := testConfig()
	c = config.Keep(c, config.KeptItem{Key: config.KeptMediaKey("series", "", 77)})
	items := []model.Media{{
		Type: model.Series, Title: "Show", SourceID: 77,
		Seasons: []model.Season{{Number: 1}, {Number: 2}},
	}}
	ApplyMedia(items, c)
	if !items[0].Protected {
		t.Fatalf("expected the kept series to be protected: %#v", items[0])
	}
	for _, season := range items[0].Seasons {
		if !season.Protected || season.ProtectionReason != items[0].ProtectionReason {
			t.Fatalf("expected every season to inherit series-wide protection: %#v", season)
		}
	}
}

func TestApplyTorrentValueProtectsBelowMinimumRatio(t *testing.T) {
	c := testConfig()
	c.Protection.MinTorrentRatio = 1.0
	items := []model.Torrent{{Name: "low", Ratio: 0.5}, {Name: "high", Ratio: 2.0}}
	ApplyTorrentValue(items, c, sufficientTorrentHistory("|"))
	if !items[0].Protected || items[0].ProtectionReason != "Below minimum ratio" {
		t.Fatalf("expected below-ratio torrent to be protected: %#v", items[0])
	}
	if items[1].Protected {
		t.Fatalf("expected above-ratio torrent to remain unprotected: %#v", items[1])
	}
}

func TestApplyTorrentValueKeptTorrentHasAnObligation(t *testing.T) {
	c := testConfig()
	items := []model.Torrent{{Name: "kept", Hash: "KEPT", Ratio: 5}, {Name: "other", Hash: "other", Ratio: 5}}
	ApplyTorrentValue(items, c, sufficientTorrentHistory("|KEPT", "|other"))
	if !items[0].Protected || items[0].Obligation != "Kept" {
		t.Fatalf("expected the kept torrent to be protected with an obligation: %#v", items[0])
	}
	if items[1].Protected || items[1].Obligation != "" {
		t.Fatalf("expected the other torrent to stay unprotected: %#v", items[1])
	}
}

func TestApplyTorrentValuePrivateSeedingDays(t *testing.T) {
	c := testConfig()
	c.Protection.PrivateMinSeedingDays = 10
	items := []model.Torrent{
		{Name: "young private", Hash: "a", Private: true, SeedingTime: 3 * 86400},
		{Name: "old private", Hash: "b", Private: true, SeedingTime: 30 * 86400},
		{Name: "young public", Hash: "c", SeedingTime: 3 * 86400},
	}
	ApplyTorrentValue(items, c, sufficientTorrentHistory("|a", "|b", "|c"))
	if items[0].Obligation == "" || items[1].Obligation != "" || items[2].Obligation != "" {
		t.Fatalf("only the young private torrent has to keep seeding: %q %q %q", items[0].Obligation, items[1].Obligation, items[2].Obligation)
	}
	c.Protection.PrivateMinSeedingDays = 0
	ApplyTorrentValue(items, c, sufficientTorrentHistory("|a", "|b", "|c"))
	if items[0].Obligation != "" {
		t.Fatalf("0 days is off: %q", items[0].Obligation)
	}
}

// A torrent too new to judge is protected, without an obligation.
func TestApplyTorrentValueInsufficientHistoryIsNoObligation(t *testing.T) {
	c := testConfig()
	items := []model.Torrent{{Name: "new", Hash: "n"}}
	ApplyTorrentValue(items, c, nil)
	if !items[0].Protected || items[0].Obligation != "" {
		t.Fatalf("expected protected without obligation: %#v", items[0])
	}
}

func TestApplyTorrentValueMinRatioZeroProtectsNothing(t *testing.T) {
	c := testConfig()
	items := []model.Torrent{{Name: "zero-ratio", Ratio: 0}}
	ApplyTorrentValue(items, c, sufficientTorrentHistory("|"))
	if items[0].Protected {
		t.Fatalf("MinTorrentRatio of zero (unset) must never protect anything: %#v", items[0])
	}
}

func TestApplyTorrentValueBothRatioAndKeepCanProtectTheSameTorrent(t *testing.T) {
	c := testConfig()
	c.Protection.MinTorrentRatio = 1.0
	items := []model.Torrent{{Name: "both", Hash: "kept", Ratio: 0.1}}
	ApplyTorrentValue(items, c, nil)
	if !items[0].Protected {
		t.Fatalf("expected torrent satisfying both protection rules to be protected: %#v", items[0])
	}
}

// Overfin's scores are the Retention Value, as Overfin gives them: a
// film's, each season's, a series' best season's. What the operator kept is protected
// without changing the value, and what Overfin didn't score is protected.
func TestOverfinScoresAreTheRetentionValue(t *testing.T) {
	c := testConfig()
	low, high := 12.5, 80.0
	items := []model.Media{
		{Type: model.Movie, TMDBID: 1, OverfinScore: &high},
		{Type: model.Movie, TMDBID: 2, OverfinScore: &low},
		{Type: model.Movie, TMDBID: 3},
		{Type: model.Movie, TMDBID: 4, SourceID: 4, OverfinScore: &low},
		{Type: model.Series, TMDBID: 5, Seasons: []model.Season{{Number: 1}, {Number: 2}, {Number: 3}},
			OverfinSeasonScores: map[int]float64{1: 20, 2: 55}},
	}
	ApplyMedia(items, c)
	by := map[int]model.Media{}
	for _, m := range items {
		by[m.TMDBID] = m
	}
	if by[1].RetentionValue != 80 || by[1].Protected || by[2].RetentionValue != 12.5 || by[2].Protected {
		t.Errorf("scored films: %+v %+v", by[1], by[2])
	}
	if !by[3].Protected || by[3].ProtectionReason != notValued {
		t.Errorf("an unscored film: %+v", by[3])
	}
	if !by[4].Protected || by[4].ProtectionReason != "Kept" || by[4].RetentionValue != 12.5 {
		t.Errorf("a kept film keeps Overfin's score: %+v", by[4])
	}
	series := by[5]
	if series.RetentionValue != 55 || series.Protected {
		t.Errorf("the series: %+v", series)
	}
	s := series.Seasons
	if s[0].RetentionValue != 20 || s[0].Protected || s[1].RetentionValue != 55 || !s[2].Protected || s[2].ProtectionReason != notValued {
		t.Errorf("its seasons: %+v", s)
	}
	if items[0].TMDBID != 2 {
		t.Errorf("the least valuable first: %+v", items[0])
	}
}

func TestTorrentsDoNotChangeOverfinsScore(t *testing.T) {
	c := testConfig()
	score := 40.0
	items := []model.Media{{Type: model.Movie, TMDBID: 1, OverfinScore: &score, Torrents: []model.Torrent{
		{AssociationStatus: model.TorrentCurrent, MediaHardlinked: true, LeechersSwarm: 50},
	}}}
	ApplyMedia(items, c)
	if items[0].RetentionValue != 40 {
		t.Fatalf("a hardlinked, busy torrent changed Overfin's score: %+v", items[0])
	}
}

func TestWithoutOverfinNothingIsValued(t *testing.T) {
	c := testConfig()
	c.Overfin.URL = ""
	items := []model.Media{{Type: model.Movie, Title: "Film"}, {Type: model.Series, Title: "Show", Seasons: []model.Season{{Number: 1}}}}
	ApplyMedia(items, c)
	for _, m := range items {
		if !m.Protected || m.ProtectionReason != notValued {
			t.Fatalf("expected everything protected without Overfin: %+v", m)
		}
	}
}

func TestSizeBreaksEqualValueTie(t *testing.T) {
	c := testConfig()
	score := 30.0
	items := []model.Media{{Title: "Small", SizeBytes: 10, OverfinScore: &score}, {Title: "Large", SizeBytes: 100, OverfinScore: &score}}
	ApplyMedia(items, c)
	if items[0].Title != "Large" {
		t.Fatalf("larger equal-value media should fall first: %#v", items)
	}
}
