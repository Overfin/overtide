// Package prowlarr is Overtide's client of Prowlarr: its apps (Radarr,
// Sonarr), whose "Prowlarr Server" address Overtide replaces with its
// proxy's, and its indexers' limits.
package prowlarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

func (client *Client) send(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx := client.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, client.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", client.key)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := client.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 2048))
		return fmt.Errorf("prowlarr %s %s: %s: %s", method, path, r.Status, bytes.TrimSpace(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(out)
}

// App is one of Prowlarr's apps, kept whole: updating it sends it back as
// it came (its masked API key, "********", is kept by Prowlarr).
type App map[string]any

func (app App) ID() int {
	id, _ := app["id"].(float64)
	return int(id)
}

func (app App) Name() string           { name, _ := app["name"].(string); return name }
func (app App) Implementation() string { kind, _ := app["implementation"].(string); return kind }

// Field returns the value of one of the app's settings, such as
// "prowlarrUrl".
func (app App) Field(name string) string {
	fields, _ := app["fields"].([]any)
	for _, raw := range fields {
		field, _ := raw.(map[string]any)
		if field["name"] == name {
			value, _ := field["value"].(string)
			return value
		}
	}
	return ""
}

// SetField sets one of the app's settings.
func (app App) SetField(name, value string) {
	fields, _ := app["fields"].([]any)
	for _, raw := range fields {
		if field, ok := raw.(map[string]any); ok && field["name"] == name {
			field["value"] = value
		}
	}
}

// Apps returns Prowlarr's apps.
func (client *Client) Apps() ([]App, error) {
	var apps []App
	return apps, client.send(http.MethodGet, "/api/v1/applications", nil, &apps)
}

// UpdateApp saves an app. Prowlarr tests it first (Radarr or Sonarr must
// reach the address it gives them) and refuses it if the test fails.
func (client *Client) UpdateApp(app App) error {
	return client.send(http.MethodPut, fmt.Sprintf("/api/v1/applications/%d", app.ID()), app, nil)
}

// SyncApps has Prowlarr rewrite its indexers in every app now, existing
// ones included.
func (client *Client) SyncApps() error {
	return client.send(http.MethodPost, "/api/v1/command", map[string]any{"name": "ApplicationIndexerSync", "forceSync": true}, nil)
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
	if err := client.send(http.MethodGet, "/api/v1/indexer", nil, &raw); err != nil {
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
