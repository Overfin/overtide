package prowlarr

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Limits come from each indexer's fields; usage adds searches and RSS.
func TestIndexersAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/indexer":
			w.Write([]byte(`[{"id":3,"name":"T","fields":[{"name":"baseSettings.queryLimit","value":50},{"name":"baseSettings.grabLimit","value":5},{"name":"baseSettings.limitsUnit","value":1}]}]`))
		case "/api/v1/indexerstats":
			if r.URL.Query().Get("startDate") == "" {
				t.Error("no start date")
			}
			w.Write([]byte(`{"indexers":[{"indexerId":3,"numberOfQueries":4,"numberOfRssQueries":2,"numberOfFailedQueries":1}]}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	client := New(srv.URL, "k")
	indexers, err := client.Indexers()
	if err != nil || len(indexers) != 1 || indexers[0].QueryLimit != 50 || indexers[0].GrabLimit != 5 || indexers[0].LimitsUnit != time.Hour {
		t.Fatalf("%v %v", indexers, err)
	}
	usage, err := client.Usage(time.Now().Add(-time.Hour))
	if err != nil || usage[3].Queries != 6 || usage[3].Failed != 1 {
		t.Fatalf("%v %v", usage, err)
	}
	if _, err := New(srv.URL, "wrong").Indexers(); err == nil {
		t.Fatal("a refused key went unnoticed")
	}
}
