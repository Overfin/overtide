package arr

import (
	"fmt"
	"net/http"
	"strings"
)

// The app's Connect entries (notifications), read and saved whole, as for
// import lists.

// Notifications returns the app's Connect entries.
func (client *Client) Notifications() ([]map[string]any, error) {
	var out []map[string]any
	return out, client.Get("/api/v3/notification", &out)
}

// WebhookSchema returns a new Webhook entry as the app makes one: every
// field at its default.
func (client *Client) WebhookSchema() (map[string]any, error) {
	var schemas []map[string]any
	if err := client.Get("/api/v3/notification/schema", &schemas); err != nil {
		return nil, err
	}
	for _, schema := range schemas {
		if implementation, _ := schema["implementation"].(string); strings.EqualFold(implementation, "Webhook") {
			return schema, nil
		}
	}
	return nil, fmt.Errorf("%s has no Webhook connection", client.app)
}

// TestNotification has the app send the entry's test event: an error when
// it couldn't, or the address answered with one.
func (client *Client) TestNotification(entry map[string]any) error {
	return client.Send(http.MethodPost, "/api/v3/notification/test", entry, nil)
}

// SaveNotification saves the entry: a new one when it has no ID.
func (client *Client) SaveNotification(entry map[string]any) error {
	if id, ok := entry["id"].(float64); ok && id > 0 {
		return client.Send(http.MethodPut, fmt.Sprintf("/api/v3/notification/%d", int(id)), entry, nil)
	}
	return client.Send(http.MethodPost, "/api/v3/notification", entry, nil)
}

// SetField sets one of an entry's fields (its "fields" list, by name).
func SetField(entry map[string]any, name string, value any) {
	fields, _ := entry["fields"].([]any)
	for _, f := range fields {
		if field, ok := f.(map[string]any); ok && field["name"] == name {
			field["value"] = value
			return
		}
	}
	entry["fields"] = append(fields, map[string]any{"name": name, "value": value})
}

// Field is one of an entry's fields, nil when it has none by that name.
func Field(entry map[string]any, name string) any {
	fields, _ := entry["fields"].([]any)
	for _, f := range fields {
		if field, ok := f.(map[string]any); ok && field["name"] == name {
			return field["value"]
		}
	}
	return nil
}
