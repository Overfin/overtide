// Package arr is what Overtide's clients of Radarr, Sonarr and Prowlarr
// share: the API key, the requests and their errors, and the parts of the
// v3 API Radarr and Sonarr have alike (tags, root folders, quality
// profiles, commands, the queue).
package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/services/redirect"
)

// Client is one app's address and API key.
type Client struct {
	// app is the app's name, as errors give it.
	app       string
	base, key string
	hc        *http.Client
	ctx       context.Context
}

// New is a client of app ("Radarr", "Sonarr", "Prowlarr") at base.
func New(app, base, key string) *Client {
	return &Client{app: app, base: strings.TrimRight(base, "/"), key: key, hc: &http.Client{Timeout: 20 * time.Second, CheckRedirect: redirect.Check}}
}

// App is the app's name.
func (client *Client) App() string { return client.app }

// Configured reports whether the client has an address.
func (client *Client) Configured() bool { return client.base != "" }

func (client *Client) WithContext(ctx context.Context) *Client {
	clientCopy := *client
	clientCopy.ctx = ctx
	return &clientCopy
}

// WithTimeout is the client, its requests bounded by timeout.
func (client *Client) WithTimeout(timeout time.Duration) *Client {
	clientCopy := *client
	hc := *client.hc
	hc.Timeout = timeout
	clientCopy.hc = &hc
	return &clientCopy
}

// SearchTimeout bounds what takes the apps long: an interactive search,
// which asks every indexer, and a big download's list of files, each of
// which is inspected.
const SearchTimeout = 3 * time.Minute

// Patient is the client for those.
func (client *Client) Patient() *Client { return client.WithTimeout(SearchTimeout) }

func (client *Client) requestContext() context.Context {
	if client.ctx != nil {
		return client.ctx
	}
	return context.Background()
}

// StatusError is the app answering with an error status.
type StatusError struct {
	App string
	// Method and Path are the request's.
	Method, Path string
	Code         int
	// Detail is the status and the answer's start.
	Detail string
	// Body is the start of the answer, for the callers that read why.
	Body []byte
}

func (e *StatusError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %s", strings.ToLower(e.App), e.Detail)
	}
	return fmt.Sprintf("%s %s %s: %s", strings.ToLower(e.App), e.Method, e.Path, e.Detail)
}

// IsNotFound reports whether err is the app saying the thing doesn't exist.
func IsNotFound(err error) bool {
	var status *StatusError
	return errors.As(err, &status) && status.Code == http.StatusNotFound
}

// maxErrorBody bounds how much of an error answer is kept.
const maxErrorBody = 64 << 10

// Send makes one request, with body as JSON when it isn't nil, and decodes
// the answer into out when out isn't nil.
func (client *Client) Send(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(client.requestContext(), method, client.base+path, reader)
	if err != nil {
		return err
	}
	request.Header.Set("X-Api-Key", client.key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.hc.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode/100 != 2 {
		answer, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		detail := answer
		if len(detail) > 512 {
			detail = detail[:512]
		}
		return &StatusError{App: client.app, Method: method, Path: path, Code: response.StatusCode, Detail: strings.TrimSpace(response.Status + " " + string(detail)), Body: answer}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// Get is Send without a body.
func (client *Client) Get(path string, out any) error {
	return client.Send(http.MethodGet, path, nil, out)
}

// Validate checks the address and the key. Without an address there's
// nothing to check.
func (client *Client) Validate() error {
	if client.base == "" {
		return nil
	}
	var status map[string]any
	return client.Get("/api/v3/system/status", &status)
}

// Tag is one of the app's tags.
type Tag struct {
	ID    int    `json:"id"`
	Label string `json:"label"`
}

// TagLabels returns the app's tags, by ID.
func (client *Client) TagLabels() (map[int]string, error) {
	var tags []Tag
	if err := client.Get("/api/v3/tag", &tags); err != nil {
		return nil, err
	}
	labels := make(map[int]string, len(tags))
	for _, t := range tags {
		labels[t.ID] = t.Label
	}
	return labels, nil
}

// Labels are the labels of the tag IDs, the unknown ones left out.
func Labels(ids []int, labels map[int]string) []string {
	out := []string{}
	for _, id := range ids {
		if label := labels[id]; label != "" {
			out = append(out, label)
		}
	}
	return out
}

// EnsureTag returns the ID of the tag with this label, made if needed.
func (client *Client) EnsureTag(label string) (int, error) {
	var tags []Tag
	if err := client.Get("/api/v3/tag", &tags); err != nil {
		return 0, err
	}
	for _, t := range tags {
		if strings.EqualFold(t.Label, label) {
			return t.ID, nil
		}
	}
	var made Tag
	return made.ID, client.Send(http.MethodPost, "/api/v3/tag", map[string]string{"label": label}, &made)
}

// RootFolder is one of the app's root folders.
type RootFolder struct {
	Path string `json:"path"`
}

// QualityProfile is one of the app's quality profiles.
type QualityProfile struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Options are the app's root folders and quality profiles, for the
// operator to choose a destination.
func (client *Client) Options() ([]string, []QualityProfile, error) {
	var folders []RootFolder
	if err := client.Get("/api/v3/rootfolder", &folders); err != nil {
		return nil, nil, err
	}
	var profiles []QualityProfile
	if err := client.Get("/api/v3/qualityprofile", &profiles); err != nil {
		return nil, nil, err
	}
	paths := make([]string, 0, len(folders))
	for _, folder := range folders {
		paths = append(paths, folder.Path)
	}
	return paths, profiles, nil
}

// Destination is where a title Overtide adds goes: the app's root folder
// and quality profile. With several of either, which one isn't Overtide's
// guess, so it's an error.
func (client *Client) Destination() (string, int, error) {
	folders, profiles, err := client.Options()
	if err != nil {
		return "", 0, err
	}
	if len(folders) != 1 || len(profiles) != 1 {
		return "", 0, fmt.Errorf("found %d root folders and %d quality profiles in %s; Overtide adds only with one of each", len(folders), len(profiles), client.app)
	}
	return folders[0], profiles[0].ID, nil
}

// StorageRoots returns the app's root folders. Without an address, none.
func (client *Client) StorageRoots() ([]string, error) {
	if client.base == "" {
		return nil, nil
	}
	var folders []RootFolder
	if err := client.Get("/api/v3/rootfolder", &folders); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(folders))
	for _, folder := range folders {
		if strings.TrimSpace(folder.Path) != "" {
			out = append(out, folder.Path)
		}
	}
	return out, nil
}

// Command starts one of the app's commands, and returns its ID to follow
// it.
func (client *Client) Command(body map[string]any) (int, error) {
	var command struct {
		ID int `json:"id"`
	}
	return command.ID, client.Send(http.MethodPost, "/api/v3/command", body, &command)
}

// CommandStatus is queued, started, completed, failed, aborted or
// cancelled.
func (client *Client) CommandStatus(commandID int) (string, error) {
	var command struct {
		Status string `json:"status"`
	}
	return command.Status, client.Get(fmt.Sprintf("/api/v3/command/%d", commandID), &command)
}

// FindQueueItem returns the queue entry id for the download with the given
// info hash, if the app currently has one — e.g. an incomplete torrent it's
// still waiting to import. Comparison is case-insensitive since the app's
// downloadId casing isn't guaranteed to match the torrent client's.
func (client *Client) FindQueueItem(hash string) (int, bool, error) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return 0, false, fmt.Errorf("hash is required")
	}
	// Pages are counted at the size asked for, not the one answered, so an
	// answer without one can't keep the search going.
	const pageSize = 250
	for page := 1; ; page++ {
		var resp struct {
			TotalRecords int `json:"totalRecords"`
			Records      []struct {
				ID         int    `json:"id"`
				DownloadID string `json:"downloadId"`
			} `json:"records"`
		}
		if err := client.Get(fmt.Sprintf("/api/v3/queue?page=%d&pageSize=%d", page, pageSize), &resp); err != nil {
			return 0, false, err
		}
		for _, rec := range resp.Records {
			if strings.EqualFold(rec.DownloadID, hash) {
				return rec.ID, true, nil
			}
		}
		if len(resp.Records) == 0 || page*pageSize >= resp.TotalRecords {
			return 0, false, nil
		}
	}
}

// RemoveQueueItem removes a queue entry and asks the app to instruct the
// download client to remove the underlying download too, rather than
// leaving an orphaned client-side download behind once the queue entry
// the app was tracking it under is gone.
func (client *Client) RemoveQueueItem(id int) error {
	return client.Send(http.MethodDelete, fmt.Sprintf("/api/v3/queue/%d?removeFromClient=true&blocklist=false", id), nil, nil)
}

// HistoryRecord is one event of the app's whole history.
type HistoryRecord struct {
	ID          int    `json:"id"`
	MovieID     int    `json:"movieId"`
	SeriesID    int    `json:"seriesId"`
	EpisodeID   int    `json:"episodeId"`
	DownloadID  string `json:"downloadId"`
	EventType   string `json:"eventType"`
	Date        string `json:"date"`
	SourceTitle string `json:"sourceTitle"`
	// Data is the import's file: where it went and its size (the apps
	// write both as strings).
	Data struct {
		ImportedPath string `json:"importedPath"`
		Size         string `json:"size"`
	} `json:"data"`
	// Episode is Sonarr's, asked for with includeEpisode.
	Episode struct {
		SeasonNumber  int `json:"seasonNumber"`
		EpisodeNumber int `json:"episodeNumber"`
	} `json:"episode"`
}

// SizeBytes is the imported file's size, 0 when the app didn't say.
func (r HistoryRecord) SizeBytes() int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(r.Data.Size), 10, 64)
	return max(n, 0)
}

// ImportsSince walks the history newest-first, gives each import's record
// to add, and stops at records older than since. A zero since performs the
// one-time full bootstrap. Equal timestamps are intentionally replayed; the
// database de-duplicates them. It returns the newest record's date.
func (client *Client) ImportsSince(since time.Time, add func(HistoryRecord, time.Time)) (time.Time, error) {
	var newest time.Time
	const pageSize = 1000
	for page := 1; ; page++ {
		var hp struct {
			TotalRecords int             `json:"totalRecords"`
			Records      []HistoryRecord `json:"records"`
		}
		path := fmt.Sprintf("/api/v3/history?page=%d&pageSize=%d&sortKey=date&sortDirection=descending&includeEpisode=true", page, pageSize)
		if err := client.Get(path, &hp); err != nil {
			return time.Time{}, err
		}
		for _, r := range hp.Records {
			d, err := time.Parse(time.RFC3339Nano, r.Date)
			if err != nil {
				d, _ = time.Parse(time.RFC3339, r.Date)
			}
			if !d.IsZero() && newest.IsZero() {
				newest = d
			}
			if !since.IsZero() && !d.IsZero() && d.Before(since) {
				return newest, nil
			}
			if !strings.EqualFold(r.EventType, "downloadFolderImported") || strings.TrimSpace(r.DownloadID) == "" || d.IsZero() {
				continue
			}
			r.DownloadID = strings.ToLower(strings.TrimSpace(r.DownloadID))
			add(r, d)
		}
		if len(hp.Records) == 0 || page*pageSize >= hp.TotalRecords {
			return newest, nil
		}
	}
}
