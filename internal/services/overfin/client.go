// Package overfin reads Overfin's title values: one score per film and per
// season, from 0 to 100, for what the libraries have, and its suggestions
// (the titles the libraries lack, in the order to add them). Overfin
// weighs what people watch, ask for and like; Overtide weighs that score
// against storage and torrents. The key needs Overfin's "Values" scope.
// It also reads the titles people wished for, in turn order, saying back
// what became of each ("Wishes to fetch").
package overfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/redirect"
)

type Client struct {
	base, key string
	hc        *http.Client
	ctx       context.Context
}

func New(base, key string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), key: key, hc: &http.Client{Timeout: 30 * time.Second, CheckRedirect: redirect.Check}}
}

func (client *Client) WithContext(ctx context.Context) *Client {
	clientCopy := *client
	clientCopy.ctx = ctx
	return &clientCopy
}

func (client *Client) requestContext() context.Context {
	if client.ctx != nil {
		return client.ctx
	}
	return context.Background()
}

// pageSize is how many titles one request asks for: Overfin's most.
const pageSize = 1000

// maxPages bounds one fetch, so a server that keeps answering "next" can't
// keep Overtide reading forever.
const maxPages = 1000

type page struct {
	Titles []struct {
		Kind   string   `json:"kind"`
		Name   string   `json:"name"`
		TMDB   string   `json:"tmdbId"`
		TVDB   string   `json:"tvdbId"`
		Season *int     `json:"season"`
		Score  *float64 `json:"score"`
		// Categories are the operator's it fits (list=suggestions).
		Categories []string `json:"categories"`
	} `json:"titles"`
	Next string `json:"next"`
}

// Scores are Overfin's values for what the libraries have, by kind and ID.
type Scores struct {
	movies        map[int]float64 // by TMDB ID
	seasonsByTMDB map[int]map[int]float64
	seasonsByTVDB map[int]map[int]float64
}

// scopeOf is the scope of Overfin's Apps a path needs, as Overfin's panel
// names it.
func scopeOf(path string) string {
	switch {
	case strings.HasPrefix(path, "/apps/v1/wishes"):
		return "Wishes to fetch"
	case strings.HasPrefix(path, "/apps/v1/history"):
		return "What came and went"
	}
	return "Values"
}

func (client *Client) get(path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(client.requestContext(), http.MethodGet, client.base+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+client.key)
	req.Header.Set("Accept", "application/json")
	resp, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	scope := scopeOf(path)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("overfin refused the key: make one in its control panel, under Apps, with %s ticked", scope)
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("overfin's key may not read this: tick %s for it in Overfin's Apps", scope)
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("overfin %s: %s", path, resp.Status)
	}
	// Bounded: the largest real answers (a whole library) are tens of MB.
	return json.NewDecoder(io.LimitReader(resp.Body, maxAnswer)).Decode(out)
}

// Fetch reads every score of what the libraries have.
func (client *Client) Fetch() (*Scores, error) {
	scores := &Scores{movies: map[int]float64{}, seasonsByTMDB: map[int]map[int]float64{}, seasonsByTVDB: map[int]map[int]float64{}}
	cursor := ""
	for range maxPages {
		q := url.Values{"inLibrary": {"true"}, "fields": {"score"}, "sort": {"name"}, "limit": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var p page
		if err := client.get("/apps/v1/titles", q, &p); err != nil {
			return nil, err
		}
		for _, t := range p.Titles {
			if t.Score == nil {
				continue
			}
			tmdb, _ := strconv.Atoi(t.TMDB)
			tvdb, _ := strconv.Atoi(t.TVDB)
			switch {
			case t.Kind == "movie" && tmdb > 0:
				scores.movies[tmdb] = *t.Score
			case t.Kind == "season" && t.Season != nil:
				if tmdb > 0 {
					addSeason(scores.seasonsByTMDB, tmdb, *t.Season, *t.Score)
				}
				if tvdb > 0 {
					addSeason(scores.seasonsByTVDB, tvdb, *t.Season, *t.Score)
				}
			}
		}
		if p.Next == "" {
			return scores, nil
		}
		cursor = p.Next
	}
	return nil, fmt.Errorf("overfin titles: more than %d pages", maxPages)
}

func addSeason(m map[int]map[int]float64, id, season int, score float64) {
	if m[id] == nil {
		m[id] = map[int]float64{}
	}
	m[id][season] = score
}

// Apply gives each film its score and each series its seasons' scores, by
// TMDB ID, else (series) TVDB ID. What Overfin didn't score is left
// without one.
func (scores *Scores) Apply(items []model.Media) {
	for i := range items {
		m := &items[i]
		m.OverfinScore, m.OverfinSeasonScores = nil, nil
		switch m.Type {
		case model.Movie:
			if s, ok := scores.movies[m.TMDBID]; ok && m.TMDBID > 0 {
				m.OverfinScore = &s
			}
		case model.Series:
			seasons := scores.seasonsByTMDB[m.TMDBID]
			if m.TMDBID == 0 || seasons == nil {
				seasons = scores.seasonsByTVDB[m.TVDBID]
				if m.TVDBID == 0 {
					seasons = nil
				}
			}
			if len(seasons) > 0 {
				m.OverfinSeasonScores = map[int]float64{}
				for n, s := range seasons {
					m.OverfinSeasonScores[n] = s
				}
			}
		}
	}
}

// Apply fetches the scores and applies them to items.
func (client *Client) Apply(items []model.Media) error {
	if client.base == "" || client.key == "" {
		return nil
	}
	scores, err := client.Fetch()
	if err != nil {
		return err
	}
	scores.Apply(items)
	return nil
}

// Validate checks the address and that the key may read values.
func (client *Client) Validate() error {
	if client.base == "" || client.key == "" {
		return nil
	}
	var p page
	return client.get("/apps/v1/titles", url.Values{"fields": {"score"}, "limit": {"1"}}, &p)
}

// Missing is a title the libraries lack, as Overfin suggests it: a film,
// or a series with the seasons Overfin valued.
type Missing struct {
	Kind model.MediaType
	Name string
	TMDB int
	TVDB int // series; 0 when Overfin doesn't know it
	// Seasons are the series' seasons Overfin suggests, in its order.
	Seasons []int
	// Score is the film's, or the series' best season's.
	Score float64
	// Categories are the operator's categories it fits (their IDs).
	Categories []string
}

// Values are the library's films by TMDB ID and seasons by TheTVDB ID, with
// their scores.
type Values struct {
	Films   map[int]float64
	Seasons map[int]map[int]float64
}

// LibraryValues reads the scores of what the libraries have, films by
// TMDB ID and seasons by TheTVDB ID. Without an Overfin, it's empty.
func (client *Client) LibraryValues() (Values, error) {
	if client.base == "" || client.key == "" {
		return Values{Films: map[int]float64{}, Seasons: map[int]map[int]float64{}}, nil
	}
	scores, err := client.Fetch()
	if err != nil {
		return Values{Films: map[int]float64{}, Seasons: map[int]map[int]float64{}}, err
	}
	return Values{Films: scores.movies, Seasons: scores.seasonsByTVDB}, nil
}

// Suggestions are at most max of the titles the libraries lack, in the
// order Overfin says to add them (list=suggestions: its picks by value). A
// series comes once, at its first season's place.
func (client *Client) Suggestions(max int) ([]Missing, error) {
	if client.base == "" || client.key == "" {
		return nil, nil
	}
	var out []Missing
	series := map[int]int{} // TMDB ID → index in out
	cursor := ""
	for range maxPages {
		q := url.Values{"list": {"suggestions"}, "fields": {"score"}, "limit": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var p page
		if err := client.get("/apps/v1/titles", q, &p); err != nil {
			return nil, err
		}
		for _, t := range p.Titles {
			tmdb, _ := strconv.Atoi(t.TMDB)
			tvdb, _ := strconv.Atoi(t.TVDB)
			score := 0.0
			if t.Score != nil {
				score = *t.Score
			}
			switch {
			case tmdb <= 0:
			case t.Kind == "movie":
				if len(out) < max {
					out = append(out, Missing{Kind: model.Movie, Name: t.Name, TMDB: tmdb, Score: score, Categories: t.Categories})
				}
			case t.Kind == "season":
				season := 0
				if t.Season != nil {
					season = *t.Season
				}
				if i, ok := series[tmdb]; ok {
					out[i].Score = math.Max(out[i].Score, score)
					out[i].Seasons = append(out[i].Seasons, season)
				} else if len(out) < max {
					series[tmdb] = len(out)
					out = append(out, Missing{Kind: model.Series, Name: t.Name, TMDB: tmdb, TVDB: tvdb, Seasons: []int{season}, Score: score, Categories: t.Categories})
				}
			}
		}
		if p.Next == "" || len(out) >= max {
			return out, nil
		}
		cursor = p.Next
	}
	return out, nil
}

// Left is a film or a series that left the libraries, at its last
// departure: a film by its TMDB ID, a series (one of its seasons) by its
// TheTVDB ID.
type Left struct {
	Kind model.MediaType
	ID   int
	At   time.Time
}

// LeftSince returns what left the libraries since then (GET
// /apps/v1/history, the key's history scope), each title once, at its
// last departure.
func (client *Client) LeftSince(since time.Time) ([]Left, error) {
	if client.base == "" || client.key == "" {
		return nil, nil
	}
	type page struct {
		Events []struct {
			Kind string `json:"kind"`
			TMDB string `json:"tmdbId"`
			TVDB string `json:"tvdbId"`
			At   string `json:"at"`
		} `json:"events"`
		Next string `json:"next"`
	}
	last := map[string]Left{}
	var order []string
	cursor := ""
	for range maxPages {
		q := url.Values{"since": {since.UTC().Format(time.RFC3339)}, "event": {"left"}, "limit": {"1000"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var p page
		if err := client.get("/apps/v1/history", q, &p); err != nil {
			return nil, err
		}
		for _, e := range p.Events {
			at, err := time.Parse(time.RFC3339, e.At)
			if err != nil {
				continue
			}
			left := Left{At: at}
			switch e.Kind {
			case "movie":
				left.Kind = model.Movie
				left.ID, _ = strconv.Atoi(e.TMDB)
			case "season":
				left.Kind = model.Series
				left.ID, _ = strconv.Atoi(e.TVDB)
			}
			if left.ID <= 0 {
				continue
			}
			key := string(left.Kind) + ":" + strconv.Itoa(left.ID)
			if _, seen := last[key]; !seen {
				order = append(order, key)
			}
			if at.After(last[key].At) {
				last[key] = left
			}
		}
		if p.Next == "" {
			break
		}
		cursor = p.Next
	}
	out := make([]Left, 0, len(order))
	for _, key := range order {
		out = append(out, last[key])
	}
	return out, nil
}

// Category is one of the operator's categories: its ID, and its name in
// each language.
type Category struct {
	ID    string            `json:"id"`
	Names map[string]string `json:"names"`
}

// Name is the category's name in English, else in any language.
func (category Category) Name() string {
	if name := category.Names["en"]; name != "" {
		return name
	}
	for _, name := range category.Names {
		return name
	}
	return category.ID
}

// Categories returns the operator's categories, in their order.
func (client *Client) Categories() ([]Category, error) {
	if client.base == "" || client.key == "" {
		return nil, nil
	}
	var answer struct {
		Categories []Category `json:"categories"`
	}
	return answer.Categories, client.get("/apps/v1/categories", url.Values{}, &answer)
}

// Wishes are the titles people wished for, in the order Overfin's turns
// give them (its "Wishes to fetch" scope): films, and series with the
// seasons wished for, a series once, at its first season's place. Never
// who wished them. Empty when Overfin's wishes go elsewhere.
func (client *Client) Wishes(max int) ([]Missing, error) {
	if client.base == "" || client.key == "" {
		return nil, nil
	}
	var p page
	if err := client.get("/apps/v1/wishes", url.Values{"limit": {strconv.Itoa(max)}}, &p); err != nil {
		return nil, err
	}
	var out []Missing
	series := map[int]int{} // TMDB ID → index in out
	for _, t := range p.Titles {
		tmdb, _ := strconv.Atoi(t.TMDB)
		tvdb, _ := strconv.Atoi(t.TVDB)
		switch {
		case tmdb <= 0:
		case t.Kind == "movie":
			out = append(out, Missing{Kind: model.Movie, Name: t.Name, TMDB: tmdb, Categories: t.Categories})
		case t.Kind == "season" && t.Season != nil:
			if i, ok := series[tmdb]; ok {
				out[i].Seasons = append(out[i].Seasons, *t.Season)
				continue
			}
			series[tmdb] = len(out)
			out = append(out, Missing{Kind: model.Series, Name: t.Name, TMDB: tmdb, TVDB: tvdb, Seasons: []int{*t.Season}, Categories: t.Categories})
		}
	}
	return out, nil
}

// Wish states, as Overfin takes them.
const (
	WishWaiting     = "waiting"
	WishNotFound    = "notFound"
	WishUnreleased  = "unreleased"
	WishDownloading = "downloading"
)

// ErrNotWished: no one wishes for the title any more.
var ErrNotWished = errors.New("overfin: no one wishes for this title")

// WishStatus tells Overfin what became of a wished film (season nil) or
// season: a search, found or not, spends the turn of whoever wished it.
// nextTry goes with WishNotFound, releaseDate (YYYY-MM-DD, or "") with
// WishUnreleased.
func (client *Client) WishStatus(tmdb int, season *int, state string, nextTry time.Time, releaseDate string) error {
	body := map[string]any{"kind": "movie", "tmdbId": strconv.Itoa(tmdb), "state": state}
	if season != nil {
		body["kind"], body["season"] = "season", *season
	}
	if state == WishNotFound && !nextTry.IsZero() {
		body["nextTry"] = nextTry.UTC().Format(time.RFC3339)
	}
	if state == WishUnreleased && releaseDate != "" {
		body["releaseDate"] = releaseDate
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(client.requestContext(), http.MethodPost, client.base+"/apps/v1/wishes/status", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+client.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotWished
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("overfin refused the key: it needs \"Wishes to fetch\" in Overfin's Apps")
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("overfin wish status: %s", resp.Status)
	}
	return nil
}

// EpisodeOrders is what TMDB says of a series' episodes.
type EpisodeOrders struct {
	Seasons []OrderSeason `json:"seasons"`
}

// OrderSeason is one season in TMDB's own order. Rearranged: another order
// on TMDB puts other episodes in its places.
type OrderSeason struct {
	Season     int            `json:"season"`
	Rearranged bool           `json:"rearranged"`
	Episodes   []OrderEpisode `json:"episodes"`
}

// OrderEpisode is one episode: Name in English, Translated in the server's
// language, when that isn't English.
type OrderEpisode struct {
	Episode    int    `json:"episode"`
	AirDate    string `json:"airDate"`
	Name       string `json:"name"`
	Translated string `json:"translated"`
}

// EpisodeOrders asks Overfin what TMDB says of a series' episodes. nil
// without an error when Overfin isn't connected.
func (client *Client) EpisodeOrders(tmdbID int) (*EpisodeOrders, error) {
	if client.base == "" || client.key == "" {
		return nil, nil
	}
	var answer EpisodeOrders
	if err := client.get("/apps/v1/episode-orders", url.Values{"tmdbId": {strconv.Itoa(tmdbID)}}, &answer); err != nil {
		return nil, err
	}
	return &answer, nil
}

// Server asks which Overfin answers: its version (GET /apps/v1/server).
// An Overfin older than that route answers ErrNotFound-like errors.
func (client *Client) Server() (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := client.get("/apps/v1/server", nil, &out); err != nil {
		return "", err
	}
	return out.Version, nil
}

// maxAnswer bounds a successful answer read whole.
const maxAnswer = 512 << 20
