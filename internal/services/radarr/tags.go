package radarr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Tags: Keep sets and takes off overtide-keep on films.

// StatusError is Radarr answering with an error status.
type StatusError struct {
	Code   int
	Detail string
}

func (e *StatusError) Error() string { return fmt.Sprintf("radarr: %d %s", e.Code, e.Detail) }

// send makes one request, with body as JSON when it isn't nil, and decodes
// the answer into out when out isn't nil.
func (client *Client) send(method, path string, body, out any) error {
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
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return &StatusError{Code: response.StatusCode, Detail: strings.TrimSpace(response.Status + " " + string(detail))}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// EnsureTag returns the ID of the tag with this label, made if needed.
func (client *Client) EnsureTag(label string) (int, error) {
	var tags []tag
	if err := client.send(http.MethodGet, "/api/v3/tag", nil, &tags); err != nil {
		return 0, err
	}
	for _, t := range tags {
		if strings.EqualFold(t.Label, label) {
			return t.ID, nil
		}
	}
	var made tag
	return made.ID, client.send(http.MethodPost, "/api/v3/tag", map[string]string{"label": label}, &made)
}

// EditTags adds the tag to the films, or takes it off them, in one call.
func (client *Client) EditTags(ids []int, tagID int, add bool) error {
	if len(ids) == 0 {
		return nil
	}
	apply := "remove"
	if add {
		apply = "add"
	}
	return client.send(http.MethodPut, "/api/v3/movie/editor", map[string]any{"movieIds": ids, "tags": []int{tagID}, "applyTags": apply}, nil)
}
