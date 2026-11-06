package arr

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newClient(url string) *Client { return New("Radarr", url, "key") }

func TestFindQueueItemMatchesByDownloadID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/queue" {
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageSize":     250,
			"totalRecords": 1,
			"records":      []any{map[string]any{"id": 7, "downloadId": "ABC123"}},
		})
	}))
	defer server.Close()
	id, ok, err := newClient(server.URL).FindQueueItem("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || id != 7 {
		t.Fatalf("expected match id 7, got id=%d ok=%v", id, ok)
	}
}

func TestFindQueueItemNoMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pageSize":     250,
			"totalRecords": 1,
			"records":      []any{map[string]any{"id": 7, "downloadId": "OTHER"}},
		})
	}))
	defer server.Close()
	_, ok, err := newClient(server.URL).FindQueueItem("abc123")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no match")
	}
}

func TestRemoveQueueItemRemovesFromClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v3/queue/7" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("removeFromClient") != "true" {
			t.Fatalf("expected removeFromClient=true, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	if err := newClient(server.URL).RemoveQueueItem(7); err != nil {
		t.Fatal(err)
	}
}

// An answer without a pageSize doesn't make the queue search ask forever:
// it stops after the pages its 250-record pages cover.
func TestFindQueueItemStopsWithoutAPageSize(t *testing.T) {
	asked := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		if asked > 20 {
			http.Error(w, "too many pages asked", http.StatusTeapot)
			return
		}
		fmt.Fprintf(w, `{"totalRecords":600,"records":[{"id":%d,"downloadId":"other"}]}`, asked)
	}))
	defer server.Close()
	id, found, err := newClient(server.URL).FindQueueItem("abc")
	if err != nil || found || id != 0 {
		t.Fatalf("id %d, found %v, err %v after %d pages", id, found, err, asked)
	}
	if asked != 3 {
		t.Fatalf("asked %d pages, want 3", asked)
	}
}

// An error answer keeps its body for the callers that read why, and its
// start for the message.
func TestStatusErrorKeepsTheAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `[{"errorMessage":"%0600d"}]`, 0)
	}))
	defer server.Close()
	err := newClient(server.URL).Get("/api/v3/x", nil)
	status, ok := err.(*StatusError)
	if !ok || status.Code != http.StatusBadRequest || len(status.Body) < 600 || len(status.Detail) > 600 {
		t.Fatalf("%#v", err)
	}
	var answer []map[string]string
	if json.Unmarshal(status.Body, &answer) != nil || IsNotFound(err) {
		t.Fatalf("body %q", status.Body)
	}
}

func TestPatientWaitsLonger(t *testing.T) {
	client := newClient("http://x")
	if client.Patient().hc.Timeout != SearchTimeout || client.hc.Timeout != 20*time.Second {
		t.Fatal("not patient, or the client changed")
	}
}
