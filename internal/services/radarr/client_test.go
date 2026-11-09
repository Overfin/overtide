package radarr

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAddImportListExclusion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v3/exclusions" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["movieTitle"] != "Movie" || payload["tmdbId"] != float64(42) || payload["movieYear"] != float64(2026) {
			t.Fatalf("unexpected payload: %#v", payload)
		}
		response.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	if err := New(server.URL, "key").AddImportListExclusion("Movie", 2026, 42); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryFailsClosedWhenTagsAreUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/tag" {
			http.Error(w, "broken", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "key").Inventory(); err == nil {
		t.Fatal("missing protection tags must make inventory unreliable")
	}
}

// Asked again for an exclusion it has, Radarr answers 400 (this body is a real
// Radarr 6's): the exclusion is there, as wanted. Any other 400 is a failure.
func TestExclusionAlreadyThere(t *testing.T) {
	body := `[{"propertyName":"TmdbId","errorMessage":"This exclusion has already been added.","errorCode":"ImportListExclusionExistsValidator"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(body))
	}))
	defer server.Close()
	client := New(server.URL, "k")
	if err := client.AddImportListExclusion("A", 2001, 42); err != nil {
		t.Errorf("already there: %v", err)
	}
	body = `[{"errorMessage":"Something else"}]`
	if err := client.AddImportListExclusion("A", 2001, 42); err == nil {
		t.Error("another refusal should be an error")
	}
}
