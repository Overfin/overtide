package sonarrdriver

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// airedMargin keeps the episodes aired just now out of the counts Sonarr and
// TMDB are compared on: they may disagree on them by a day.
const airedMargin = 48 * time.Hour

// waitingFile is a file of a download that isn't imported, because nothing
// says which episode it is.
type waitingFile struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// episodeMark is where a file's name gives its number: S03E01 or 3x01, with
// more episodes after it for a file holding several.
var episodeMark = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:s\d{1,3}[ ._-]?e\d{1,4}(?:[ ._-]?e\d{1,4})*|\d{1,3}x\d{2,4})(?:[^a-z0-9]|$)`)

// seasonMark gives the season in a file's name.
var seasonMark = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:s(\d{1,3})[ ._-]?e\d|(\d{1,3})x\d{2})`)

// fileSeason is the season a file's name says, -1 when it doesn't.
func fileSeason(file string) int {
	match := seasonMark.FindStringSubmatch(path.Base(strings.ReplaceAll(file, `\`, "/")))
	if match == nil {
		return -1
	}
	number, err := strconv.Atoi(match[1] + match[2])
	if err != nil {
		return -1
	}
	return number
}

// standaloneNumber finds numbers on their own in a file's name.
var standaloneNumber = regexp.MustCompile(`(?:^|[ ._-])(\d{1,4})(?:[ ._-]|$)`)

// seriesYear is the year Sonarr adds to some series' titles.
var seriesYear = regexp.MustCompile(` \(\d{4}\)$`)

// fileAbsolute is the absolute number in a name like "One Piece 783 MULTI":
// the number right after the series' title, with no season mark. 0 when it
// has none. Nothing but the series' title may come before it, so a number
// that's part of a longer name isn't taken for one.
func fileAbsolute(file, series string) int {
	name := path.Base(strings.ReplaceAll(file, `\`, "/"))
	name = strings.TrimSuffix(name, path.Ext(name))
	if seasonMark.MatchString(name) {
		return 0
	}
	want := comparableTitle(seriesYear.ReplaceAllString(series, ""))
	if want == "" {
		return 0
	}
	for _, at := range standaloneNumber.FindAllStringSubmatchIndex(name, -1) {
		if comparableTitle(name[:at[0]]) == want {
			number, err := strconv.Atoi(name[at[2]:at[3]])
			if err != nil {
				return 0
			}
			return number
		}
	}
	return 0
}

// placeAbsolute puts an anime's files named by absolute number on their
// episodes: such a number means one episode, whatever the seasons' order.
func placeAbsolute(files []sonarr.ImportItem, episodes []sonarr.Episode, series string) []sonarr.ImportItem {
	byAbsolute := map[int]int{}
	for _, e := range episodes {
		if e.AbsoluteNumber > 0 {
			byAbsolute[e.AbsoluteNumber] = e.ID
		}
	}
	out := slices.Clone(files)
	for i := range out {
		if id, ok := byAbsolute[fileAbsolute(out[i].Path, series)]; ok {
			out[i].Episodes = []sonarr.ImportEpisode{{ID: id}}
			out[i].ByAbsolute = true
		}
	}
	return out
}

// releaseWords start what comes after an episode's title in a file's name.
var releaseWords = regexp.MustCompile(`(?i)[ ._-](?:multi|vff|vfq|vfi|vf2?|vostfr|french|truefrench|english|custom|ad|repack|proper|final|internal|uncut|extended|\d{3,4}p|web(?:-?dl|rip)?|blu-?ray|bdrip|hdtv|hdlight|amzn|nf|dsnp|hmax|atvp|hulu|x26[45]|h\.?26[45]|hevc|av1|dv|hdr|sdr|ddp?\d|e?ac3|aac|opus|10bits?)(?:[ ._-]|$)`)

// fileTitle is the episode title in a file's name, "" when it has none.
func fileTitle(file string) string {
	name := path.Base(strings.ReplaceAll(file, `\`, "/"))
	name = strings.TrimSuffix(name, path.Ext(name))
	mark := episodeMark.FindStringIndex(name)
	if mark == nil {
		return ""
	}
	rest := name[mark[1]:]
	// What's before the release's words, those included if they come first.
	if at := releaseWords.FindStringIndex(" " + rest); at != nil {
		rest = (" " + rest)[:at[0]]
	}
	return strings.Trim(strings.NewReplacer(".", " ", "_", " ").Replace(rest), " -")
}

// accents are the letters a title may be written with or without.
var accents = strings.NewReplacer("à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "æ", "ae", "ç", "c", "è", "e", "é", "e", "ê", "e", "ë", "e", "ì", "i", "í", "i", "î", "i", "ï", "i", "ñ", "n", "ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "œ", "oe", "ù", "u", "ú", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y", "ß", "ss")

// comparableTitle is a title without case, accents, spaces or punctuation.
func comparableTitle(title string) string {
	title = accents.Replace(strings.ToLower(title))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, title)
}

// noTitle matches what stands in for a title, which says nothing.
var noTitle = regexp.MustCompile(`^(?:(?:episode|episodio|chapter|chapitre|part|partie)?\d+|tba|tbd)$`)

// titleIndex is, by comparable title, the episodes that have it.
type titleIndex map[string][]int

func (index titleIndex) add(title string, id int) {
	key := comparableTitle(title)
	if key == "" || noTitle.MatchString(key) || slices.Contains(index[key], id) {
		return
	}
	index[key] = append(index[key], id)
}

// seasonCheck says whether a season's numbers can be believed: nothing else
// can be meant by them. It returns why not, "" when they can.
func seasonCheck(season int, episodes []sonarr.Episode, orders *overfin.EpisodeOrders, now time.Time) string {
	if orders == nil {
		return "Overfin can't say what its number means, and its name has no title to go by."
	}
	var tmdb *overfin.OrderSeason
	for i := range orders.Seasons {
		if orders.Seasons[i].Season == season {
			tmdb = &orders.Seasons[i]
		}
	}
	if tmdb == nil {
		return fmt.Sprintf("TMDB has no season %d, and its name has no title to go by.", season)
	}
	if tmdb.Rearranged {
		return fmt.Sprintf("TMDB lists another order for season %d, and its name has no title to tell them apart.", season)
	}
	cutoff := now.Add(-airedMargin)
	sonarrAired, tmdbAired := 0, 0
	for _, e := range episodes {
		if e.SeasonNumber == season && !e.AirDateUTC.IsZero() && e.AirDateUTC.Before(cutoff) {
			sonarrAired++
		}
	}
	for _, e := range tmdb.Episodes {
		if aired, err := time.Parse("2006-01-02", e.AirDate); err == nil && aired.Before(cutoff) {
			tmdbAired++
		}
	}
	if sonarrAired != tmdbAired {
		return fmt.Sprintf("Sonarr and TMDB disagree on season %d (%d and %d episodes aired), and its name has no title to go by.", season, sonarrAired, tmdbAired)
	}
	return ""
}

// titlesOf indexes the titles a file's name may give: Sonarr's, and TMDB's in
// the server's language, each for the Sonarr episode it means. TMDB's episode
// is Sonarr's by their English title, or by number in a season whose numbers
// can be believed.
func titlesOf(episodes []sonarr.Episode, orders *overfin.EpisodeOrders, now time.Time) titleIndex {
	index := titleIndex{}
	byNumber := map[[2]int]int{}
	for _, e := range episodes {
		index.add(e.Title, e.ID)
		byNumber[[2]int{e.SeasonNumber, e.EpisodeNumber}] = e.ID
	}
	if orders == nil {
		return index
	}
	sonarrTitles := titleIndex{}
	for _, e := range episodes {
		sonarrTitles.add(e.Title, e.ID)
	}
	for _, season := range orders.Seasons {
		numbersHold := seasonCheck(season.Season, episodes, orders, now) == ""
		for _, e := range season.Episodes {
			var id int
			if same := sonarrTitles[comparableTitle(e.Name)]; len(same) == 1 {
				id = same[0]
			} else if numbersHold {
				id = byNumber[[2]int{season.Season, e.Episode}]
			}
			if id != 0 {
				index.add(e.Name, id)
				index.add(e.Translated, id)
			}
		}
	}
	return index
}

// isSample reports whether Sonarr takes a file for a sample of the release.
func isSample(item sonarr.ImportItem) bool {
	return slices.ContainsFunc(item.Rejections, func(r sonarr.ImportRejection) bool {
		return strings.Contains(strings.ToLower(r.Reason), "sample")
	})
}

// notAnUpgrade reports whether Sonarr finds a file no better than what the
// episode has.
func notAnUpgrade(item sonarr.ImportItem) bool {
	return slices.ContainsFunc(item.Rejections, func(r sonarr.ImportRejection) bool {
		return strings.Contains(strings.ToLower(r.Reason), "upgrade")
	})
}

// identify says which files of a download are which episodes. A title in the
// file's name wins over its number; a number is believed when nothing else
// can be meant by it (seasonCheck) and the download doesn't hold more files
// for its season than the season has episodes. Any other file waits: nothing
// is imported on a guess. Samples are left out. So is a lone file no better
// than what its episode has, as Sonarr's own import would (a whole season
// replaces what it finds, so the season comes from one release).
func identify(files []sonarr.ImportItem, episodes []sonarr.Episode, orders *overfin.EpisodeOrders, now time.Time) (imports []sonarr.ImportItem, waiting []waitingFile) {
	index := titlesOf(episodes, orders, now)
	inSeason := map[int]int{}
	for _, e := range episodes {
		inSeason[e.SeasonNumber]++
	}
	var kept []sonarr.ImportItem
	for _, item := range files {
		if !isSample(item) {
			kept = append(kept, item)
		}
	}
	if len(kept) == 1 && notAnUpgrade(kept[0]) {
		return nil, nil
	}
	// The files for each season, by their names: Sonarr gives no season to
	// those past the end of one, which is what shows a pack in another order.
	claimed := map[int]int{}
	for _, item := range kept {
		if item.SeasonNumber != nil && *item.SeasonNumber != 0 {
			claimed[*item.SeasonNumber]++
		} else if season := fileSeason(item.Path); season >= 0 {
			claimed[season]++
		}
	}

	type choice struct {
		item     sonarr.ImportItem
		episodes []int
		byTitle  bool
		why      string
	}
	guessed := func(item sonarr.ImportItem) []int {
		ids := make([]int, 0, len(item.Episodes))
		for _, e := range item.Episodes {
			ids = append(ids, e.ID)
		}
		return ids
	}
	choices := make([]choice, 0, len(kept))
	for _, item := range kept {
		c := choice{item: item}
		ids := guessed(item)
		if item.ByAbsolute {
			c.byTitle, c.episodes = true, ids
			choices = append(choices, c)
			continue
		}
		if match := index[comparableTitle(fileTitle(item.Path))]; len(match) == 1 {
			c.byTitle = true
			c.episodes = match
			// A file of several episodes keeps them, the title being one's.
			if len(ids) > 1 && slices.Contains(ids, match[0]) {
				c.episodes = ids
			}
			choices = append(choices, c)
			continue
		}
		switch {
		case item.SeasonNumber == nil || len(ids) == 0:
			c.why = "Its number matches none of Sonarr's episodes, and its name no episode's title."
		case claimed[*item.SeasonNumber] > inSeason[*item.SeasonNumber]:
			c.why = fmt.Sprintf("The download has %d files for season %d, which has %d episodes in Sonarr, and its name has no title to go by.", claimed[*item.SeasonNumber], *item.SeasonNumber, inSeason[*item.SeasonNumber])
		default:
			c.why = seasonCheck(*item.SeasonNumber, episodes, orders, now)
			if c.why == "" {
				c.episodes = ids
			}
		}
		choices = append(choices, c)
	}

	// Two files for one episode: a title beats a number, and otherwise
	// neither is believed.
	byTitle, byNumber := map[int]int{}, map[int]int{}
	for _, c := range choices {
		for _, id := range c.episodes {
			if c.byTitle {
				byTitle[id]++
			} else {
				byNumber[id]++
			}
		}
	}
	for i := range choices {
		c := &choices[i]
		for _, id := range c.episodes {
			if c.byTitle && byTitle[id] > 1 {
				c.why = "Another file's title says it's the same episode."
			}
			if !c.byTitle && byTitle[id] > 0 {
				c.why = "Another file's title says it's this episode."
			}
			if !c.byTitle && byNumber[id] > 1 {
				c.why = "Another file has the same number."
			}
		}
	}
	for _, c := range choices {
		if c.why != "" {
			waiting = append(waiting, waitingFile{Path: c.item.Path, Why: c.why})
			continue
		}
		item := c.item
		item.Episodes = make([]sonarr.ImportEpisode, 0, len(c.episodes))
		for _, id := range c.episodes {
			item.Episodes = append(item.Episodes, sonarr.ImportEpisode{ID: id})
		}
		imports = append(imports, item)
	}
	return imports, waiting
}
