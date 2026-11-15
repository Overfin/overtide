package sonarrdriver

import (
	"fmt"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/sonarr"
)

func run(minutes ...int) []lengthEpisode {
	out := make([]lengthEpisode, len(minutes))
	for i, m := range minutes {
		out[i] = lengthEpisode{ID: i + 1, Minutes: m}
	}
	return out
}

// Each file takes the one run of episodes its length fits: segments in
// pairs, a double-length special alone, files one to one; a length two
// runs fit, or none, or an unknown runtime settles nothing.
func TestFitLengths(t *testing.T) {
	for _, c := range []struct {
		name    string
		lengths []float64
		order   []lengthEpisode
		want    string
	}{
		{"segments", []float64{22.4, 21.9}, run(11, 11, 11, 11), "[[1 2] [3 4]]"},
		{"a special among segments", []float64{22, 23, 22}, run(11, 11, 22, 11, 11), "[[1 2] [3] [4 5]]"},
		{"one to one, the end missing", []float64{44, 43}, run(44, 44, 44), "[[1] [2]]"},
		{"two runs fit", []float64{21}, run(20, 2), "no"},
		{"none fits", []float64{17}, run(11, 11, 11, 11), "no"},
		{"runtime unknown", []float64{22}, run(0, 11), "no"},
		{"more files than episodes", []float64{11, 11, 11}, run(11, 11), "no"},
	} {
		got, ok := fitLengths(c.lengths, c.order)
		s := fmt.Sprint(got)
		if !ok {
			s = "no"
		}
		if s != c.want {
			t.Errorf("%s: %s, want %s", c.name, s, c.want)
		}
	}
}

// The release's order is the one every file fits; two orders placing the
// files differently settle nothing.
func TestFitOrders(t *testing.T) {
	broadcast := []lengthEpisode{{ID: 1, Minutes: 70}, {ID: 2, Minutes: 70}, {ID: 3, Minutes: 70}}
	netflix := []lengthEpisode{{ID: 4, Minutes: 45}, {ID: 5, Minutes: 46}, {ID: 6, Minutes: 44}}
	got, ok := fitOrders([]float64{45, 46, 44}, [][]lengthEpisode{broadcast, netflix})
	if !ok || fmt.Sprint(got) != "[[4] [5] [6]]" {
		t.Errorf("Netflix's order: %v %v", got, ok)
	}
	other := []lengthEpisode{{ID: 7, Minutes: 45}, {ID: 8, Minutes: 45}, {ID: 9, Minutes: 45}}
	if _, ok := fitOrders([]float64{45, 46, 44}, [][]lengthEpisode{netflix, other}); ok {
		t.Error("two orders fitting differently settled it")
	}
}

// placeByLength takes the files in the order their names number them,
// with Sonarr's episodes and TMDB's runtimes (matched by title).
func TestPlaceByLength(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	aired := now.AddDate(-1, 0, 0)
	var episodes []sonarr.Episode
	var tmdb []overfin.OrderEpisode
	for n := 1; n <= 4; n++ {
		title := fmt.Sprintf("Segment %c", 'A'+n-1)
		episodes = append(episodes, sonarr.Episode{ID: 100 + n, SeasonNumber: 1, EpisodeNumber: n, Title: title, AirDateUTC: aired, AirDate: aired.Format("2006-01-02")})
		tmdb = append(tmdb, overfin.OrderEpisode{Episode: n, Name: title, AirDate: aired.Format("2006-01-02"), Runtime: 11})
	}
	orders := &overfin.EpisodeOrders{Seasons: []overfin.OrderSeason{{Season: 1, Episodes: tmdb}}}
	files := []string{"/dl/Show.S01E02.mkv", "/dl/Show.S01E01.mkv"}
	lengths := map[string]float64{"/dl/Show.S01E01.mkv": 22.1, "/dl/Show.S01E02.mkv": 21.8}
	got, ok := placeByLength(1, files, lengths, episodes, orders, now)
	if !ok || fmt.Sprint(got["/dl/Show.S01E01.mkv"]) != "[101 102]" || fmt.Sprint(got["/dl/Show.S01E02.mkv"]) != "[103 104]" {
		t.Fatalf("placed: %v %v", got, ok)
	}
	delete(lengths, "/dl/Show.S01E02.mkv")
	if _, ok := placeByLength(1, files, lengths, episodes, orders, now); ok {
		t.Error("placed with a length unknown")
	}
}

// A release of 22-minute files where Sonarr and TMDB count 11-minute
// segments: the numbers would be believed (both count the same), but the
// lengths show two segments a file, and place them so. A release whose
// lengths match its numbers one to one keeps its numbers.
func TestIdentifySegments(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	episodes := sonarrEpisodes(now, map[int]int{1: 4})
	orders := tmdbOrders(now, map[int]int{1: 4})
	for i := range orders.Seasons[0].Episodes {
		orders.Seasons[0].Episodes[i].Runtime = 11
	}
	files := []sonarr.ImportItem{packFile("Show.S01E01.mkv", 1, 101), packFile("Show.S01E02.mkv", 1, 102)}
	lengths := map[string]float64{"/downloads/pack/Show.S01E01.mkv": 22.3, "/downloads/pack/Show.S01E02.mkv": 21.7}
	imports, waiting := identifyWith(files, episodes, orders, lengths, now)
	got := chosen(imports)
	if len(waiting) != 0 || fmt.Sprint(got["Show.S01E01.mkv"]) != "[101 102]" || fmt.Sprint(got["Show.S01E02.mkv"]) != "[103 104]" {
		t.Fatalf("segments: %v, waiting %v", got, waiting)
	}
	// One file per episode: its number stands.
	lengths = map[string]float64{"/downloads/pack/Show.S01E01.mkv": 11.2, "/downloads/pack/Show.S01E02.mkv": 10.9}
	imports, _ = identifyWith(files, episodes, orders, lengths, now)
	if got := chosen(imports); fmt.Sprint(got["Show.S01E02.mkv"]) != "[102]" {
		t.Errorf("one to one: %v", got)
	}
	// No lengths: as before.
	imports, _ = identifyWith(files, episodes, orders, nil, now)
	if got := chosen(imports); fmt.Sprint(got["Show.S01E02.mkv"]) != "[102]" {
		t.Errorf("without lengths: %v", got)
	}
}
