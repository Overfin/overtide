package cleanup

import (
	"fmt"
	"testing"

	"github.com/overfin/overtide/internal/model"
)

func TestBuildUnavailableStorage(t *testing.T) {
	p, err := Build("/definitely/not/a/overtide/storage/path", 0, 90, 95, nil, nil, true, nil, false)
	if err == nil {
		t.Fatal("expected storage probe error")
	}
	if p.Available {
		t.Fatal("unavailable storage must not be marked available")
	}
	if p.Path == "" || p.Error == "" {
		t.Fatalf("expected path and error in unavailable plan: %#v", p)
	}
	if p.SelectedBytes != 0 || len(p.Actions) != 0 {
		t.Fatal("unavailable storage must not produce a cleanup selection")
	}
}

func TestBuildUsesPhysicalReclaimabilityAndSkipsProtectedMedia(t *testing.T) {
	items := []model.Media{
		{Title: "Protected", Protected: true, ReclaimableKnown: true, ReclaimableBytes: 1000, SizeBytes: 1000},
		{Title: "Shared", ReclaimableKnown: true, ReclaimableBytes: 0, SizeBytes: 2000},
		{Title: "Physical", ReclaimableKnown: true, ReclaimableBytes: 7, SizeBytes: 3000},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, items, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Kind != StandaloneMedia || p.Actions[0].Media.Title != "Physical" || p.SelectedBytes != 7 {
		t.Fatalf("cleanup did not use safe physical bytes: %#v", p)
	}
}

func TestBuildPausesWhenValuationIsUnreliable(t *testing.T) {
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{{Title: "unsafe", SizeBytes: 1}}, nil, false, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Reliable || len(p.Actions) != 0 || p.SelectedBytes != 0 {
		t.Fatalf("unreliable valuation produced cleanup candidates: %#v", p)
	}
}

// TestBuildScopesThresholdsToUsableSpace guards the whole point of the
// otherBytes parameter: target/critical must mean "percent of what Overtide
// actually has to work with" on a shared pool, not percent of the raw disk
// — otherwise space other services hold would silently count against
// Overtide's own thresholds. Every assertion is derived from a single Build
// call's own internally-consistent fields rather than compared against a
// second, separately-timed call: two independent statfs snapshots on a busy
// machine (e.g. a CI runner) can legitimately disagree by a few bytes,
// which would make a cross-call exact-equality assertion flaky without any
// real bug present.
func TestBuildScopesThresholdsToUsableSpace(t *testing.T) {
	dir := t.TempDir()
	// otherBytes is small relative to any real mounted filesystem's actual
	// usage (always at least several MB on a real OS), so it exercises the
	// normal (non-clamped) subtraction path deterministically without
	// depending on the test machine's exact disk state.
	const otherBytes = 4096
	p, err := Build(dir, otherBytes, 0, 0, nil, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Available {
		t.Skip("test filesystem stats unavailable")
	}
	if p.UsableBytes != p.TotalBytes-otherBytes {
		t.Fatalf("expected UsableBytes to exclude otherBytes: %#v", p)
	}
	if p.OvertideUsedBytes != p.UsedBytes-otherBytes {
		t.Fatalf("expected OvertideUsedBytes to exclude otherBytes: %#v", p)
	}
	// With target 0%, NeedBytes is exactly OvertideUsedBytes (see
	// TestBuildUsesTargetWithoutWaitingForCritical for the same trick) — so
	// this fails if NeedBytes were ever computed against raw UsedBytes
	// instead of the otherBytes-scoped OvertideUsedBytes.
	if p.NeedBytes != p.OvertideUsedBytes {
		t.Fatalf("expected NeedBytes to equal OvertideUsedBytes at target 0%%: %#v", p)
	}
}

func TestBuildUsesTargetWithoutWaitingForCritical(t *testing.T) {
	items := []model.Media{{Title: "candidate", ReclaimableKnown: true, ReclaimableBytes: 1}}
	p, err := Build(t.TempDir(), 0, 0, 99.999, items, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.NeedBytes == 0 || len(p.Actions) != 1 {
		t.Fatalf("critical incorrectly gated target planning: %#v", p)
	}
}

// TestBuildExcludedCandidateIsReplacedByTheNextBest guards the actual
// point of the excluded parameter: excluding the item that would
// otherwise have been selected must not just shrink the selection (and
// therefore undershoot NeedBytes) — a different eligible candidate must
// take its place so the target still gets met. Each item's own
// ReclaimableBytes is deliberately astronomically larger than any real
// test machine's actual disk usage could ever be (unlike NeedBytes
// itself, which is a real, unpredictable statfs result and must never be
// compared across two separate calls — see the flaky-CI-test fix this
// same package already went through), so selecting any single one of
// them alone is guaranteed to already exceed NeedBytes regardless of the
// test machine's real state, making "exactly one gets picked, in
// ascending-value order" deterministic without depending on disk state.
func TestBuildExcludedCandidateIsReplacedByTheNextBest(t *testing.T) {
	const hugeReclaimable = 1 << 50
	lowest := model.Media{Title: "Lowest value", ServiceID: "radarr-1", SourceID: 1, RetentionValue: 1, ReclaimableKnown: true, ReclaimableBytes: hugeReclaimable}
	middle := model.Media{Title: "Middle value", ServiceID: "radarr-1", SourceID: 2, RetentionValue: 2, ReclaimableKnown: true, ReclaimableBytes: hugeReclaimable}
	highest := model.Media{Title: "Highest value", ServiceID: "radarr-1", SourceID: 3, RetentionValue: 3, ReclaimableKnown: true, ReclaimableBytes: hugeReclaimable}
	items := []model.Media{highest, lowest, middle}

	baseline, err := Build(t.TempDir(), 0, 0, 0, items, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !baseline.Available {
		t.Skip("test filesystem stats unavailable")
	}
	if len(baseline.Actions) != 1 || baseline.Actions[0].Media.Title != "Lowest value" {
		t.Fatalf("expected only the lowest-value item selected initially: %#v", baseline.Actions)
	}

	excluded := map[string]bool{ActionKey(baseline.Actions[0]): true}
	replaced, err := Build(t.TempDir(), 0, 0, 0, items, nil, true, excluded, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced.Actions) != 1 || replaced.Actions[0].Media.Title != "Middle value" {
		t.Fatalf("expected the next-best item to replace the excluded one: %#v", replaced.Actions)
	}
	if replaced.SelectedBytes < replaced.NeedBytes {
		t.Fatalf("expected the replacement to still meet NeedBytes: selected=%d need=%d", replaced.SelectedBytes, replaced.NeedBytes)
	}
}

func TestBuildLocksHardlinkedMediaAndTorrentIntoOneBundle(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", Name: "Release", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, LastActivity: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 {
		t.Fatalf("expected exactly one bundled action, got %#v", p.Actions)
	}
	action := p.Actions[0]
	if action.Kind != HardlinkedBundle || action.Media.Title != "Movie" || len(action.Torrents) != 1 || action.Torrents[0].Hash != "abc" {
		t.Fatalf("expected media+torrent bundle, got %#v", action)
	}
	if action.Value != 2 {
		t.Fatalf("expected the bundle's value to be the title's alone, torrents don't weigh: got %v", action.Value)
	}
	if action.ReclaimableBytes != 900 {
		t.Fatalf("expected bundle to report the combined reclaim estimate, not either side alone: %#v", action)
	}
}

func TestBuildNeverOffersEitherSideOfAHardlinkedBundleAloneWhenMediaIsProtected(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, LastActivity: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", Protected: true, RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("protected media must lock out its hardlinked torrent entirely, got %#v", p.Actions)
	}
}

func TestBuildNeverOffersEitherSideOfAHardlinkedBundleAloneWhenBundleEstimateIsUnknown(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, LastActivity: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: false, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("unknown bundle estimate must lock out the hardlinked torrent entirely, got %#v", p.Actions)
	}
}

func TestBuildExcludesProtectedStandaloneTorrent(t *testing.T) {
	torrent := model.Torrent{Hash: "old", AssociationStatus: model.TorrentSuperseded, Protected: true, LastActivity: 1, ReclaimableKnown: true, ReclaimableBytes: 100}
	p, err := Build(t.TempDir(), 0, 0, 0, nil, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a protected torrent must never appear in the plan, got %#v", p.Actions)
	}
}

func TestBuildBlocksWholeBundleWhenAHardlinkedTorrentHasAnObligation(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, Protected: true, Obligation: "Kept", LastActivity: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a hardlinked torrent with an obligation must hold the whole bundle back, got %#v", p.Actions)
	}
}

// A hardlinked torrent protected only for being too new to judge doesn't
// hold its title back: torrents don't weigh on a title.
func TestBuildDoesNotBlockBundleForATorrentMerelyTooNew(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, Protected: true, ProtectionReason: "Insufficient history", ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Kind != HardlinkedBundle {
		t.Fatalf("expected the bundle to stay a candidate, got %#v", p.Actions)
	}
}

// TestBuildExcludesMediaAndTorrentsFromNonOptedInServices guards a real
// bug: a standalone Media/Torrent item's own service not having checked
// "Allow automatic removal" must exclude it from the plan entirely — for
// manual review on the Storage page just as much as automatic execution,
// since both read the same Plan.Actions. RemovalRestricted is deliberately
// a separate field from Protected (see model.Media) so this isn't shown as
// a KeepTag/ratio-style "Protected" judgment elsewhere in the UI.
func TestBuildExcludesMediaAndTorrentsFromNonOptedInServices(t *testing.T) {
	media := model.Media{Title: "Restricted Movie", RemovalRestricted: true, ReclaimableKnown: true, ReclaimableBytes: 100, SizeBytes: 100}
	torrent := model.Torrent{Hash: "old", AssociationStatus: model.TorrentSuperseded, RemovalRestricted: true, ReclaimableKnown: true, ReclaimableBytes: 100}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a removal-restricted media item and torrent must never appear in the plan, got %#v", p.Actions)
	}
}

// TestBuildBlocksWholeBundleWhenEitherSideIsRemovalRestricted guards the
// bidirectional case: a bundle spans a Media service and one or more
// Torrent services, and either side not being opted in must veto the whole
// bundle, the same as either side being Protected already does — offering
// only the opted-in side alone would still delete files the other side
// needs.
func TestBuildBlocksWholeBundleWhenEitherSideIsRemovalRestricted(t *testing.T) {
	torrent := model.Torrent{Hash: "abc", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, RemovalRestricted: true, LastActivity: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	media := model.Media{Title: "Movie", RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{torrent}}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a removal-restricted hardlinked torrent must veto the whole bundle, got %#v", p.Actions)
	}

	restrictedMediaTorrent := model.Torrent{Hash: "def", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, LastActivity: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	restrictedMedia := model.Media{Title: "Movie 2", RemovalRestricted: true, RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900, Torrents: []model.Torrent{restrictedMediaTorrent}}
	p2, err := Build(t.TempDir(), 0, 0, 0, []model.Media{restrictedMedia}, []model.Torrent{restrictedMediaTorrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p2.Actions) != 0 {
		t.Fatalf("a removal-restricted media item must veto its own hardlinked bundle too, got %#v", p2.Actions)
	}
}

func TestBuildRanksSeriesPerSeasonInsteadOfWholeSeries(t *testing.T) {
	series := model.Media{
		Type: model.Series, Title: "Show", RetentionValue: 999,
		Seasons: []model.Season{
			{Number: 1, ReclaimableKnown: true, ReclaimableBytes: 100, RetentionValue: 1},
			{Number: 2, ReclaimableKnown: true, ReclaimableBytes: 100, RetentionValue: 2},
		},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{series}, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 {
		t.Fatalf("expected one action per season, got %#v", p.Actions)
	}
	for _, action := range p.Actions {
		if action.Kind != StandaloneSeason || action.Season == nil {
			t.Fatalf("expected a StandaloneSeason action, never a whole-series action, got %#v", action)
		}
	}
	if p.Actions[0].Season.Number != 1 || p.Actions[1].Season.Number != 2 {
		t.Fatalf("expected seasons ranked ascending by Retention Value, got %#v", p.Actions)
	}
}

// TestBuildEnforcesSeasonOrderEvenWhenValueInverts guards the safety net on
// top of season Retention Value: an earlier season must never be selected
// for removal after a later one from the same show, even if their computed
// values are inverted or tied (e.g. missing/bad air-date data) — a show's
// most recent content is meant to be kept the longest.
func TestBuildEnforcesSeasonOrderEvenWhenValueInverts(t *testing.T) {
	series := model.Media{
		Type: model.Series, Title: "Show",
		Seasons: []model.Season{
			// Season 2 has a *lower* value than season 1 here, on purpose —
			// this must not let season 2 rank ahead of (be removed before)
			// season 1 despite that.
			{Number: 1, ReclaimableKnown: true, ReclaimableBytes: 100, RetentionValue: 50},
			{Number: 2, ReclaimableKnown: true, ReclaimableBytes: 100, RetentionValue: 1},
		},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{series}, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 {
		t.Fatalf("expected one action per season, got %#v", p.Actions)
	}
	if p.Actions[0].Season.Number != 1 || p.Actions[1].Season.Number != 2 {
		t.Fatalf("expected season 1 ranked before season 2 regardless of Retention Value, got %#v", p.Actions)
	}
}

func TestBuildSkipsProtectedOrUnknownSeasons(t *testing.T) {
	series := model.Media{
		Type: model.Series, Title: "Show",
		Seasons: []model.Season{
			{Number: 1, Protected: true, ReclaimableKnown: true, ReclaimableBytes: 100},
			{Number: 2, ReclaimableKnown: false, ReclaimableBytes: 100},
			{Number: 3, ReclaimableKnown: true, ReclaimableBytes: 0},
			{Number: 4, RemovalRestricted: true, ReclaimableKnown: true, ReclaimableBytes: 100},
		},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{series}, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("expected protected/unknown/zero-byte/removal-restricted seasons to produce no actions, got %#v", p.Actions)
	}
}

func TestBuildLocksHardlinkedSeasonBundle(t *testing.T) {
	torrent := model.Torrent{Hash: "s1", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: true, HardlinkedSeasons: []int{1}, LastActivity: 5, ReclaimableKnown: true, ReclaimableBytes: 100}
	series := model.Media{
		Type: model.Series, Title: "Show", RetentionValue: 1,
		Torrents: []model.Torrent{torrent},
		Seasons:  []model.Season{{Number: 1, RetentionValue: 2, BundleReclaimableKnown: true, BundleReclaimableBytes: 900}},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{series}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Kind != HardlinkedBundle || p.Actions[0].Season == nil || p.Actions[0].Season.Number != 1 {
		t.Fatalf("expected one season-scoped hardlinked bundle, got %#v", p.Actions)
	}
	if len(p.Actions[0].Torrents) != 1 || p.Actions[0].Torrents[0].Hash != "s1" {
		t.Fatalf("expected the season-hardlinked torrent bundled in, got %#v", p.Actions[0])
	}
}

func TestBuildMoviesAreUnaffectedBySeasonLogic(t *testing.T) {
	movie := model.Media{Type: model.Movie, Title: "Film", RetentionValue: 1, ReclaimableKnown: true, ReclaimableBytes: 100}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{movie}, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 || p.Actions[0].Kind != StandaloneMedia {
		t.Fatalf("expected a movie with no Seasons to still produce a whole-media action, got %#v", p.Actions)
	}
}

func TestBuildTreatsProvenNonHardlinkedCurrentTorrentAsIndependent(t *testing.T) {
	torrent := model.Torrent{Hash: "copy", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, MediaHardlinked: false, LastActivity: 3, ReclaimableKnown: true, ReclaimableBytes: 50}
	media := model.Media{Title: "Movie", RetentionValue: 1, ReclaimableKnown: true, ReclaimableBytes: 200}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var sawMedia, sawTorrent bool
	for _, action := range p.Actions {
		switch action.Kind {
		case StandaloneMedia:
			sawMedia = true
		case StandaloneTorrent:
			sawTorrent = true
		}
	}
	if !sawMedia || !sawTorrent {
		t.Fatalf("expected independent media and torrent actions, got %#v", p.Actions)
	}
}

func TestBuildTreatsSupersededAndUnassociatedTorrentsAsStandaloneRegardlessOfHardlinkFields(t *testing.T) {
	torrents := []model.Torrent{
		{Hash: "old", AssociationStatus: model.TorrentSuperseded, LastActivity: 1, ReclaimableKnown: true, ReclaimableBytes: 10},
		{Hash: "orphan", AssociationStatus: model.TorrentUnassociated, LastActivity: 2, ReclaimableKnown: true, ReclaimableBytes: 10},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, nil, torrents, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 2 || p.Actions[0].Kind != StandaloneTorrent || p.Actions[1].Kind != StandaloneTorrent {
		t.Fatalf("expected both historical torrents as standalone actions, got %#v", p.Actions)
	}
}

func TestBuildExcludesCurrentTorrentWithUnknownHardlinkStatus(t *testing.T) {
	torrent := model.Torrent{Hash: "unknown", AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: false, LastActivity: 1, ReclaimableKnown: true, ReclaimableBytes: 100}
	p, err := Build(t.TempDir(), 0, 0, 0, nil, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("a Current torrent with unproven hardlink status must never be offered alone: %#v", p.Actions)
	}
}

func TestBuildSortsLoneTorrentsByLastActivity(t *testing.T) {
	torrents := []model.Torrent{
		{Hash: "high", AssociationStatus: model.TorrentUnassociated, LastActivity: 9, ReclaimableKnown: true, ReclaimableBytes: 1},
		{Hash: "low", AssociationStatus: model.TorrentUnassociated, LastActivity: 1, ReclaimableKnown: true, ReclaimableBytes: 1},
		{Hash: "mid", AssociationStatus: model.TorrentUnassociated, LastActivity: 5, ReclaimableKnown: true, ReclaimableBytes: 1},
	}
	p, err := Build(t.TempDir(), 0, 0, 90, nil, torrents, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 3 {
		t.Fatalf("expected all three torrents to be selected, got %#v", p.Actions)
	}
	wantOrder := []string{"low", "mid", "high"}
	for i, hash := range wantOrder {
		if p.Actions[i].Torrents[0].Hash != hash {
			t.Fatalf("expected the longest inactive first %v, got %#v", wantOrder, p.Actions)
		}
	}
}

// Lone torrents back nothing in the library: they all come before any
// title, whatever their values.
func TestBuildPutsLoneTorrentsBeforeTitles(t *testing.T) {
	torrent := model.Torrent{Hash: "old", AssociationStatus: model.TorrentSuperseded, LastActivity: 500, ReclaimableKnown: true, ReclaimableBytes: 10}
	media := model.Media{Title: "Movie", RetentionValue: 1, ReclaimableKnown: true, ReclaimableBytes: 10}
	p, err := Build(t.TempDir(), 0, 0, 0, []model.Media{media}, []model.Torrent{torrent}, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) < 2 || p.Actions[0].Kind != StandaloneTorrent || p.Actions[1].Kind != StandaloneMedia {
		t.Fatalf("expected the lone torrent first, then the title, got %#v", p.Actions)
	}
}

// Titles go lowest Overfin score first, across services: no share per
// service, the larger first on a tie.
func TestBuildOrdersTitlesByValueAcrossServices(t *testing.T) {
	media := []model.Media{
		{Title: "Series high", ServiceName: "Series", RetentionValue: 40, ReclaimableKnown: true, ReclaimableBytes: 10},
		{Title: "Movie low", ServiceName: "Movies", RetentionValue: 10, ReclaimableKnown: true, ReclaimableBytes: 10},
		{Title: "Series low small", ServiceName: "Series", RetentionValue: 10, ReclaimableKnown: true, ReclaimableBytes: 5},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, media, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range p.Actions {
		got = append(got, a.Media.Title)
	}
	want := []string{"Movie low", "Series low small", "Series high"}
	if len(got) < 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("order: got %v, want %v", got, want)
	}
}

// Lone torrents sharing files (cross-seeds) are one action, as recently
// active as their most recently active torrent, freeing what the group
// frees together.
func TestBuildGroupsLoneCrossSeeds(t *testing.T) {
	torrents := []model.Torrent{
		{Hash: "bbb", AssociationStatus: model.TorrentUnassociated, LastActivity: 30, ReclaimableKnown: true, SharesFilesWith: []string{"aaa"}, GroupReclaimableKnown: true, GroupReclaimableBytes: 1000},
		{Hash: "aaa", AssociationStatus: model.TorrentSuperseded, LastActivity: 10, ReclaimableKnown: true, SharesFilesWith: []string{"bbb"}, GroupReclaimableKnown: true, GroupReclaimableBytes: 1000},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, nil, torrents, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 1 {
		t.Fatalf("expected one action for the group, got %#v", p.Actions)
	}
	a := p.Actions[0]
	if len(a.Torrents) != 2 || a.Torrents[0].Hash != "aaa" || a.ReclaimableBytes != 1000 || a.LastActivity != 30 {
		t.Fatalf("group action: %#v", a)
	}
}

// One protected torrent keeps the whole group.
func TestBuildSkipsCrossSeedGroupWithAProtectedTorrent(t *testing.T) {
	torrents := []model.Torrent{
		{Hash: "aaa", AssociationStatus: model.TorrentSuperseded, ReclaimableKnown: true, SharesFilesWith: []string{"bbb"}, GroupReclaimableKnown: true, GroupReclaimableBytes: 1000},
		{Hash: "bbb", AssociationStatus: model.TorrentUnassociated, Protected: true, ReclaimableKnown: true, SharesFilesWith: []string{"aaa"}, GroupReclaimableKnown: true, GroupReclaimableBytes: 1000},
	}
	p, err := Build(t.TempDir(), 0, 0, 0, nil, torrents, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Actions) != 0 {
		t.Fatalf("expected nothing, got %#v", p.Actions)
	}
}

// When only part of a show is needed, its first seasons go, even if a later
// one scores lower.
func TestSeasonOrderBeforeTheCutoff(t *testing.T) {
	show := model.Media{Type: model.Series, ServiceID: "s", SourceID: 1}
	season := func(number int, value float64) Action {
		return Action{Kind: StandaloneSeason, Media: show, Season: &model.Season{Number: number}, Value: value, ReclaimableBytes: 100}
	}
	ordered := order([]Action{season(1, 50), season(2, 40), season(3, 10)}, nil, false)
	enforceSeasonOrder(ordered)
	taken := takeUntil(ordered, 100)
	if len(taken) != 1 || taken[0].Season.Number != 1 {
		t.Fatalf("expected season 1 alone, got %#v", taken)
	}
}

// A copy of a title goes before every title by default, among the lone
// torrents by activity; with copiesWithTitles, by its title's score. A
// torrent backing nothing goes first either way.
func TestCopiesOrder(t *testing.T) {
	movie := func(id int, value float64) model.Media {
		return model.Media{Type: model.Movie, ServiceID: "r", SourceID: id, RetentionValue: value, ReclaimableKnown: true, ReclaimableBytes: 100, Seasons: nil}
	}
	media := []model.Media{movie(1, 10), movie(2, 80)}
	copyOf := func(hash string, id int) model.Torrent {
		return model.Torrent{Hash: hash, AssociationStatus: model.TorrentCurrent, MediaHardlinkKnown: true, ReclaimableKnown: true, ReclaimableBytes: 50, LastActivity: 10,
			MediaItems: []model.MediaRef{{Type: model.Movie, ServiceID: "r", SourceID: id}}}
	}
	torrents := []model.Torrent{copyOf("copy-of-2", 2), {Hash: "old", AssociationStatus: model.TorrentSuperseded, ReclaimableKnown: true, ReclaimableBytes: 50, LastActivity: 5}}
	mediaTier, torrentTier := classify(media, torrents)

	keys := func(actions []Action) []string {
		var out []string
		for _, a := range actions {
			if a.Kind == StandaloneTorrent {
				out = append(out, a.Torrents[0].Hash)
			} else {
				out = append(out, fmt.Sprint(a.Media.SourceID))
			}
		}
		return out
	}
	if got := fmt.Sprint(keys(order(mediaTier, torrentTier, false))); got != "[old copy-of-2 1 2]" {
		t.Fatalf("copies first: %s", got)
	}
	if got := fmt.Sprint(keys(order(mediaTier, torrentTier, true))); got != "[old 1 copy-of-2 2]" {
		t.Fatalf("copies with titles: %s", got)
	}
}
