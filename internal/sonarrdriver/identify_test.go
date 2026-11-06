package sonarrdriver

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/sonarr"
)

func TestFileTitle(t *testing.T) {
	for file, want := range map[string]string{
		"/d/Futurama 3x01 - Amazones amoureuses.mkv":                      "Amazones amoureuses",
		"/d/Futurama.S03E01.MULTI.1080p.WEB.H265-GROUP.mkv":               "",
		"/d/Futurama.S03E22.FINAL.MULTI.1080p.WEB.H265-GROUP.mkv":         "",
		"/d/1670 - S01E01 - L'assemblée 2160p.DV.HDR.x265-GROUP.mkv":      "L'assemblée",
		"/d/1670.S03E01.Examen.sarmate.MULTi.1080p.NF.WEB.SDR.H265-X.mkv": "Examen sarmate",
		"/d/Show.S01E02E03.The.Double.Bill.1080p.WEB.mkv":                 "The Double Bill",
		"/d/Show.S01E05.mkv":                    "",
		"/d/No number here.mkv":                 "",
		`C:\d\Show - S01E02 - Windows Path.mkv`: "Windows Path",
	} {
		if got := fileTitle(file); got != want {
			t.Errorf("%s: %q, want %q", file, got, want)
		}
	}
	for file, want := range map[string]int{"/d/S.S03E01.mkv": 3, "/d/S 12x05 - T.mkv": 12, "/d/s00e01.mkv": 0, "/d/Bonus.mkv": -1, "/d/S.2020.1080p.mkv": -1} {
		if got := fileSeason(file); got != want {
			t.Errorf("%s: season %d, want %d", file, got, want)
		}
	}
	if a, b := comparableTitle("L'Été, à Noël!"), comparableTitle("l ete a noel"); a != b {
		t.Errorf("%q and %q", a, b)
	}
}

// sonarrEpisodes makes a series of seasons with those numbers of episodes,
// titled "S<season>E<episode> title", aired long ago. IDs are 100*season+episode.
func sonarrEpisodes(now time.Time, seasons map[int]int) []sonarr.Episode {
	var out []sonarr.Episode
	for season, count := range seasons {
		for e := 1; e <= count; e++ {
			out = append(out, sonarr.Episode{ID: 100*season + e, SeasonNumber: season, EpisodeNumber: e,
				Title: "Season " + strconv.Itoa(season) + " Title " + strconv.Itoa(e), AirDateUTC: now.AddDate(-1, 0, 0)})
		}
	}
	return out
}

func tmdbOrders(now time.Time, seasons map[int]int) *overfin.EpisodeOrders {
	orders := &overfin.EpisodeOrders{}
	for season, count := range seasons {
		s := overfin.OrderSeason{Season: season}
		for e := 1; e <= count; e++ {
			s.Episodes = append(s.Episodes, overfin.OrderEpisode{Episode: e, AirDate: now.AddDate(-1, 0, 0).Format("2006-01-02"), Name: "Season " + strconv.Itoa(season) + " Title " + strconv.Itoa(e)})
		}
		orders.Seasons = append(orders.Seasons, s)
	}
	return orders
}

// packFile is a file as Sonarr's manual import lists it: its guess from the
// number in the name.
func packFile(name string, season int, guessed ...int) sonarr.ImportItem {
	item := sonarr.ImportItem{Path: "/downloads/pack/" + name}
	if season != 0 {
		item.SeasonNumber = &season
	}
	for _, id := range guessed {
		item.Episodes = append(item.Episodes, sonarr.ImportEpisode{ID: id})
	}
	return item
}

func chosen(items []sonarr.ImportItem) map[string][]int {
	out := map[string][]int{}
	for _, item := range items {
		for _, e := range item.Episodes {
			out[item.Path[len("/downloads/pack/"):]] = append(out[item.Path[len("/downloads/pack/"):]], e.ID)
		}
	}
	return out
}

func waitingNames(files []waitingFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path[len("/downloads/pack/"):])
	}
	slices.Sort(out)
	return out
}

func TestIdentify(t *testing.T) {
	now := *arrfake.Day("2026-11-01")
	seasons := map[int]int{1: 3, 2: 2}
	episodes := sonarrEpisodes(now, seasons)
	orders := tmdbOrders(now, seasons)

	// Numbers believed: one order, counts agree.
	imports, waiting := identify([]sonarr.ImportItem{packFile("S.S01E01.mkv", 1, 101), packFile("S.S01E02.mkv", 1, 102)}, episodes, orders, now)
	if got := chosen(imports); len(waiting) != 0 || len(got) != 2 || got["S.S01E02.mkv"][0] != 102 {
		t.Errorf("one order: %v, waiting %v", got, waiting)
	}

	// Overfin can't tell: the number waits, a title doesn't.
	imports, waiting = identify([]sonarr.ImportItem{packFile("S.S01E01.mkv", 1, 101), packFile("S.S01E02.Season.2.Title.1.mkv", 1, 102)}, episodes, nil, now)
	if got := chosen(imports); len(got) != 1 || got["S.S01E02.Season.2.Title.1.mkv"][0] != 201 || !slices.Equal(waitingNames(waiting), []string{"S.S01E01.mkv"}) {
		t.Errorf("no Overfin: %v, waiting %v", got, waiting)
	}

	// Another order rearranges the season.
	rearranged := tmdbOrders(now, seasons)
	for i := range rearranged.Seasons {
		rearranged.Seasons[i].Rearranged = rearranged.Seasons[i].Season == 1
	}
	imports, waiting = identify([]sonarr.ImportItem{packFile("S.S01E01.mkv", 1, 101), packFile("S.S02E01.mkv", 2, 201)}, episodes, rearranged, now)
	if got := chosen(imports); len(got) != 1 || got["S.S02E01.mkv"][0] != 201 || !slices.Equal(waitingNames(waiting), []string{"S.S01E01.mkv"}) {
		t.Errorf("rearranged: %v, waiting %v", got, waiting)
	}

	// Sonarr and TMDB disagree on how many aired, not on future episodes,
	// nor on one aired just now.
	other := tmdbOrders(now, map[int]int{1: 4, 2: 2})
	if imports, waiting = identify([]sonarr.ImportItem{packFile("S.S01E01.mkv", 1, 101)}, episodes, other, now); len(imports) != 0 || len(waiting) != 1 {
		t.Errorf("counts differ: %v, waiting %v", imports, waiting)
	}
	withNew := append(slices.Clone(episodes), sonarr.Episode{ID: 104, SeasonNumber: 1, EpisodeNumber: 4, AirDateUTC: now.Add(-time.Hour)}, sonarr.Episode{ID: 105, SeasonNumber: 1, EpisodeNumber: 5})
	if imports, waiting = identify([]sonarr.ImportItem{packFile("S.S01E04.mkv", 1, 104)}, withNew, orders, now); len(imports) != 1 || len(waiting) != 0 {
		t.Errorf("a new episode: %v, waiting %v", imports, waiting)
	}

	// A pack longer than the season, Futurama's way: every number waits,
	// though each looks fine; titles still go, across seasons.
	imports, waiting = identify([]sonarr.ImportItem{
		packFile("S.S01E01.mkv", 1, 101),
		packFile("S.S01E02.mkv", 1, 102),
		packFile("S.S01E03.mkv", 1, 103),
		packFile("S.S01E04.Season.2.Title.2.mkv", 1),
	}, episodes, orders, now)
	if got := chosen(imports); len(got) != 1 || got["S.S01E04.Season.2.Title.2.mkv"][0] != 202 || len(waiting) != 3 {
		t.Errorf("long pack: %v, waiting %v", got, waiting)
	}

	// The same, as Sonarr really lists it: it gives files past the season's
	// end no season. The numbers that look fine must wait all the same.
	past := packFile("S.S01E04.mkv", 0)
	imports, waiting = identify([]sonarr.ImportItem{packFile("S.S01E01.mkv", 1, 101), packFile("S.S01E02.mkv", 1, 102), packFile("S.S01E03.mkv", 1, 103), past}, episodes, orders, now)
	if len(imports) != 0 || len(waiting) != 4 {
		t.Errorf("long pack, no season past the end: %v, waiting %v", imports, waiting)
	}

	// No number Sonarr knows, no title.
	if imports, waiting = identify([]sonarr.ImportItem{packFile("S.S09E09.mkv", 9)}, episodes, orders, now); len(imports) != 0 || len(waiting) != 1 {
		t.Errorf("unknown: %v, waiting %v", imports, waiting)
	}
	if imports, waiting = identify([]sonarr.ImportItem{packFile("Bonus.mkv", 0)}, episodes, orders, now); len(imports) != 0 || len(waiting) != 1 {
		t.Errorf("no number at all: %v, waiting %v", imports, waiting)
	}

	// Two files for one episode: the title's wins; with two numbers or two
	// titles, none.
	imports, waiting = identify([]sonarr.ImportItem{packFile("S.S01E01.mkv", 1, 101), packFile("S.S01E02.Season.1.Title.1.mkv", 1, 102)}, episodes, orders, now)
	if got := chosen(imports); len(got) != 1 || got["S.S01E02.Season.1.Title.1.mkv"][0] != 101 || !slices.Equal(waitingNames(waiting), []string{"S.S01E01.mkv"}) {
		t.Errorf("title beats number: %v, waiting %v", got, waiting)
	}
	if imports, waiting = identify([]sonarr.ImportItem{packFile("A.S01E01.mkv", 1, 101), packFile("B.S01E01.mkv", 1, 101)}, episodes, orders, now); len(imports) != 0 || len(waiting) != 2 {
		t.Errorf("same number: %v, waiting %v", imports, waiting)
	}
	if imports, waiting = identify([]sonarr.ImportItem{packFile("A.S01E01.Season.1.Title.1.mkv", 1), packFile("B.S01E02.Season.1.Title.1.mkv", 1)}, episodes, orders, now); len(imports) != 0 || len(waiting) != 2 {
		t.Errorf("same title: %v, waiting %v", imports, waiting)
	}

	// A title two episodes share says nothing, and stand-ins for titles
	// neither.
	same := slices.Clone(episodes)
	for i := range same {
		same[i].Title = "Pilot"
	}
	same[0].Title = "Episode 1"
	if imports, waiting = identify([]sonarr.ImportItem{packFile("S.S01E01.Pilot.mkv", 1, 101)}, same, tmdbOrders(now, seasons), now); len(waiting) != 0 || chosen(imports)["S.S01E01.Pilot.mkv"][0] != 101 {
		t.Errorf("a shared title: %v, waiting %v", imports, waiting)
	}

	// Multi-episode files keep their episodes, by number or by a title of one.
	imports, _ = identify([]sonarr.ImportItem{packFile("S.S01E01E02.mkv", 1, 101, 102)}, episodes, orders, now)
	if got := chosen(imports)["S.S01E01E02.mkv"]; !slices.Equal(got, []int{101, 102}) {
		t.Errorf("two episodes: %v", got)
	}
	imports, _ = identify([]sonarr.ImportItem{packFile("S.S01E01E02.Season.1.Title.2.mkv", 1, 101, 102)}, episodes, orders, now)
	if got := chosen(imports)["S.S01E01E02.Season.1.Title.2.mkv"]; !slices.Equal(got, []int{101, 102}) {
		t.Errorf("two episodes, a title: %v", got)
	}

	// A sample is left out, and doesn't count toward the pack.
	sample := packFile("S.S01E01.sample.mkv", 1, 101)
	sample.Rejections = []sonarr.ImportRejection{{Reason: "Sample"}}
	imports, waiting = identify([]sonarr.ImportItem{sample, packFile("S.S01E01.mkv", 1, 101), packFile("S.S01E02.mkv", 1, 102), packFile("S.S01E03.mkv", 1, 103)}, episodes, orders, now)
	if len(imports) != 3 || len(waiting) != 0 {
		t.Errorf("sample: %v, waiting %v", imports, waiting)
	}

	// Not an upgrade: a lone file is dropped, a pack's goes in.
	lower := packFile("S.S01E01.mkv", 1, 101)
	lower.Rejections = []sonarr.ImportRejection{{Reason: "Not an upgrade for existing episode file(s)"}}
	if imports, waiting = identify([]sonarr.ImportItem{lower}, episodes, orders, now); len(imports) != 0 || len(waiting) != 0 {
		t.Errorf("a lone file, no upgrade: %v, waiting %v", imports, waiting)
	}
	if imports, _ = identify([]sonarr.ImportItem{lower, packFile("S.S01E02.mkv", 1, 102)}, episodes, orders, now); len(imports) != 2 {
		t.Errorf("a pack's file, no upgrade: %v", imports)
	}

	// TMDB's title in the server's language leads to Sonarr's episode, by
	// the English title even across seasons.
	translated := tmdbOrders(now, seasons)
	translated.Seasons[0].Episodes[0].Translated = "Amazones amoureuses"
	name := translated.Seasons[0].Episodes[0].Name
	var want int
	for _, e := range episodes {
		if e.Title == name {
			want = e.ID
		}
	}
	imports, waiting = identify([]sonarr.ImportItem{packFile("Show 3x01 - Amazones amoureuses.mkv", 3, 999)}, episodes, translated, now)
	if got := chosen(imports)["Show 3x01 - Amazones amoureuses.mkv"]; len(waiting) != 0 || len(got) != 1 || got[0] != want {
		t.Errorf("translated: %v, want %d, waiting %v", got, want, waiting)
	}
}

func TestAbsoluteNumbers(t *testing.T) {
	for _, c := range []struct {
		file, series string
		want         int
	}{
		{"/d/One Piece 783 MULTI ''Sanji Rentre Au Bercail''.mkv", "One Piece", 783},
		{"/d/One Piece 0878 Multi ''Le Monde''.mkv", "One Piece", 878},
		{"/d/One.Piece.1100.1080p.mkv", "One Piece", 1100},
		{"/d/Doraemon - 12.mkv", "Doraemon (2005)", 12},
		{"/d/Mob Psycho 100 - 05.mkv", "Mob Psycho 100", 5},
		{"/d/One Piece S19E04 783.mkv", "One Piece", 0},
		{"/d/Other Show 783.mkv", "One Piece", 0},
		{"/d/One Piece Film 2.mkv", "One Piece", 0},
		{"/d/One Piece.mkv", "One Piece", 0},
	} {
		if got := fileAbsolute(c.file, c.series); got != c.want {
			t.Errorf("%s: %d, want %d", c.file, got, c.want)
		}
	}
	now := *arrfake.Day("2026-11-01")
	episodes := []sonarr.Episode{{ID: 1, SeasonNumber: 19, EpisodeNumber: 4, AbsoluteNumber: 783}, {ID: 2, SeasonNumber: 19, EpisodeNumber: 5, AbsoluteNumber: 784}}
	files := placeAbsolute([]sonarr.ImportItem{
		{Path: "/downloads/pack/One Piece 783 MULTI.mkv"},
		{Path: "/downloads/pack/One Piece 784 MULTI.mkv"},
		{Path: "/downloads/pack/One Piece 999 MULTI.mkv"},
	}, episodes, "One Piece")
	imports, waiting := identify(files, episodes, nil, now)
	got := chosen(imports)
	if len(got) != 2 || got["One Piece 783 MULTI.mkv"][0] != 1 || got["One Piece 784 MULTI.mkv"][0] != 2 || !slices.Equal(waitingNames(waiting), []string{"One Piece 999 MULTI.mkv"}) {
		t.Errorf("%v, waiting %v", got, waiting)
	}
}
