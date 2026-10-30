package radarr

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	id, found, err := New(server.URL, "key").FindQueueItem("abc")
	if err != nil || found || id != 0 {
		t.Fatalf("id %d, found %v, err %v after %d pages", id, found, err, asked)
	}
	if asked != 3 {
		t.Fatalf("asked %d pages, want 3", asked)
	}
}
