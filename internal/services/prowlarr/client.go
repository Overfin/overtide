// Package prowlarr is Overtide's client of Prowlarr: its trackers'
// limits, and what each was asked, so Overtide's searches stay within
// them. Prowlarr's API is v1, so only arr's requests are used, not its v3
// calls.
package prowlarr

import (
	"context"
	"net/url"
	"time"

	"github.com/overfin/overtide/internal/services/arr"
)

type Client struct{ api *arr.Client }

func New(base, key string) *Client {
	return &Client{arr.New("Prowlarr", base, key).WithTimeout(30 * time.Second)}
}

func (client *Client) WithContext(ctx context.Context) *Client {
	return &Client{client.api.WithContext(ctx)}
}

// Indexer is one of Prowlarr's indexers and its limits (0: none).
type Indexer struct {
	ID         int
	Name       string
	QueryLimit int
	GrabLimit  int
	// LimitsUnit is how long the limits count over: an hour or a day.
	LimitsUnit time.Duration
}

// Indexers returns Prowlarr's indexers.
func (client *Client) Indexers() ([]Indexer, error) {
	var raw []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Fields []struct {
			Name  string `json:"name"`
			Value any    `json:"value"`
		} `json:"fields"`
	}
	if err := client.api.Get("/api/v1/indexer", &raw); err != nil {
		return nil, err
	}
	indexers := make([]Indexer, 0, len(raw))
	for _, r := range raw {
		indexer := Indexer{ID: r.ID, Name: r.Name, LimitsUnit: 24 * time.Hour}
		for _, field := range r.Fields {
			number, _ := field.Value.(float64)
			switch field.Name {
			case "baseSettings.queryLimit":
				indexer.QueryLimit = int(number)
			case "baseSettings.grabLimit":
				indexer.GrabLimit = int(number)
			case "baseSettings.limitsUnit":
				// Prowlarr's IndexerLimitsUnit: 0 a day, 1 an hour.
				if number == 1 {
					indexer.LimitsUnit = time.Hour
				}
			}
		}
		indexers = append(indexers, indexer)
	}
	return indexers, nil
}

// Usage is what Prowlarr counts against a tracker's query limit since a
// moment: its searches and RSS polls, whoever asked; and its failures.
type Usage struct {
	Queries int
	Failed  int
}

// Usage returns, by indexer ID, what each tracker was asked since then.
func (client *Client) Usage(since time.Time) (map[int]Usage, error) {
	var stats struct {
		Indexers []struct {
			IndexerID          int `json:"indexerId"`
			NumberOfQueries    int `json:"numberOfQueries"`
			NumberOfRssQueries int `json:"numberOfRssQueries"`
			NumberOfFailed     int `json:"numberOfFailedQueries"`
			NumberOfFailedRss  int `json:"numberOfFailedRssQueries"`
		} `json:"indexers"`
	}
	path := "/api/v1/indexerstats?startDate=" + url.QueryEscape(since.UTC().Format(time.RFC3339))
	if err := client.api.Get(path, &stats); err != nil {
		return nil, err
	}
	usage := make(map[int]Usage, len(stats.Indexers))
	for _, indexer := range stats.Indexers {
		usage[indexer.IndexerID] = Usage{Queries: indexer.NumberOfQueries + indexer.NumberOfRssQueries, Failed: indexer.NumberOfFailed + indexer.NumberOfFailedRss}
	}
	return usage, nil
}

// Validate checks the address and the key.
func (client *Client) Validate() error {
	var status map[string]any
	return client.api.Get("/api/v1/system/status", &status)
}

// Health asks Prowlarr its version and its failing health checks.
func (client *Client) Health() (string, []arr.Problem, error) {
	return client.api.Health("v1")
}
