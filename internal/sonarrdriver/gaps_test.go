package sonarrdriver

import (
	"testing"

	"github.com/overfin/overtide/internal/services/sonarr"
)

// The length rule only places files that hold the season from its first
// episode on without a gap: with E06 missing, the files after it would go
// on the wrong episodes.
func TestFromFirstWithoutGaps(t *testing.T) {
	var episodes []sonarr.Episode
	for n := 1; n <= 8; n++ {
		episodes = append(episodes, sonarr.Episode{ID: 100 + n, EpisodeNumber: n, SeasonNumber: 1})
	}
	files := func(eps ...[]int) ([]string, map[string][]int) {
		var paths []string
		current := map[string][]int{}
		for i, e := range eps {
			p := string(rune('a' + i))
			paths = append(paths, p)
			for _, n := range e {
				current[p] = append(current[p], 100+n)
			}
		}
		return paths, current
	}
	for name, c := range map[string]struct {
		eps  [][]int
		want bool
	}{
		"one by one":         {[][]int{{1}, {2}, {3}}, true},
		"two in a file":      {[][]int{{1, 2}, {3}}, true},
		"a gap":              {[][]int{{1}, {2}, {4}}, false},
		"not from the first": {[][]int{{2}, {3}}, false},
	} {
		paths, current := files(c.eps...)
		if got := fromFirstWithoutGaps(paths, current, episodes); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
