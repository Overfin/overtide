// Package overfin reads Overfin's title values: one score per film and per
// season, from 0 to 100, for what the libraries have. Overfin weighs what
// people watch, ask for and like; Overtide only weighs that score against
// storage and torrents. The key needs Overfin's "Values" scope.
package overfin

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/overfin/overtide/internal/model"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	base, key string
	hc        *http.Client
	ctx       context.Context
}

func New(base, key string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), key: key, hc: &http.Client{Timeout: 30 * time.Second}}
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
	} `json:"titles"`
	Next string `json:"next"`
}

// Scores are Overfin's values for what the libraries have, by kind and ID.
type Scores struct {
	movies        map[int]float64 // by TMDB ID
	seasonsByTMDB map[int]map[int]float64
	seasonsByTVDB map[int]map[int]float64
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
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("overfin refused the key: make one in its control panel, under Apps")
	case resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("overfin's key may not read values: tick Values for it in Overfin's Apps")
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("overfin titles: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
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

// Missing is a title the libraries lack, as Overfin suggests it: a film,
// or a series with the seasons Overfin valued.
type Missing struct {
	Kind model.MediaType
	Name string
	TMDB int
	TVDB int // series; 0 when Overfin doesn't know it
	// Score is the film's, or the series' best season's.
	Score float64
}

// Suggestions are at most max of the titles the libraries lack, in the
// order Overfin says to add them (list=suggestions: its picks by value,
// with trending and popular titles mixed in by its discovery share). A
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
					out = append(out, Missing{Kind: model.Movie, Name: t.Name, TMDB: tmdb, Score: score})
				}
			case t.Kind == "season":
				if i, ok := series[tmdb]; ok {
					out[i].Score = math.Max(out[i].Score, score)
				} else if len(out) < max {
					series[tmdb] = len(out)
					out = append(out, Missing{Kind: model.Series, Name: t.Name, TMDB: tmdb, TVDB: tvdb, Score: score})
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

// Validate checks the address and that the key may read values.
func (client *Client) Validate() error {
	if client.base == "" || client.key == "" {
		return nil
	}
	var p page
	return client.get("/apps/v1/titles", url.Values{"fields": {"score"}, "limit": {"1"}}, &p)
}
