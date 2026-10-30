package inventory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/store"
)

// Before a file leaves the disk, Radarr is asked again whether it's a
// film's: one of a film's files is refused; another file in its folder
// isn't; Radarr not answering refuses (nothing is removed on a guess).
func TestVerifyUnmanagedAsksRadarr(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "Movies", "Film (2020)")
	var down atomic.Bool
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/movie":
			json.NewEncoder(w).Encode([]map[string]any{{"id": 7, "title": "Film", "year": 2020, "path": folder}})
		case "/api/v3/tag":
			w.Write([]byte("[]"))
		case "/api/v3/moviefile":
			if r.URL.Query().Get("movieId") != "7" {
				w.Write([]byte("[]"))
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{{"id": 70, "movieId": 7, "relativePath": "Film (2020).mkv", "size": 4}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fake.Close()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := New(config.Config{Services: []config.Service{{ID: "radarr-1", Type: "radarr", Name: "Movies", URL: fake.URL, APIKey: "k"}}}, database)
	ctx := context.Background()

	err = service.VerifyUnmanagedContext(ctx, []string{filepath.Join(folder, "Film (2020).mkv")})
	if err == nil || !strings.Contains(err.Error(), "claimed by Radarr") {
		t.Errorf("the film's file: %v", err)
	}
	if err := service.VerifyUnmanagedContext(ctx, []string{filepath.Join(folder, "sample.mkv")}); err != nil {
		t.Errorf("another file in its folder: %v", err)
	}
	if err := service.VerifyUnmanagedContext(ctx, []string{filepath.Join(dir, "elsewhere.mkv")}); err != nil {
		t.Errorf("a file outside every film's folder: %v", err)
	}
	down.Store(true)
	if err := service.VerifyUnmanagedContext(ctx, []string{filepath.Join(dir, "elsewhere.mkv")}); err == nil {
		t.Error("Radarr down: verified anyway")
	}
}

// The same for Sonarr: one of a series' episode files is refused, another
// file in its folder isn't.
func TestVerifyUnmanagedAsksSonarr(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "TV", "Show")
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/series":
			json.NewEncoder(w).Encode([]map[string]any{{"id": 3, "title": "Show", "year": 2020, "path": folder}})
		case "/api/v3/episodefile":
			json.NewEncoder(w).Encode([]map[string]any{{"id": 30, "seriesId": 3, "relativePath": "Season 01/Show S01E01.mkv", "size": 4}})
		default:
			w.Write([]byte("[]"))
		}
	}))
	defer fake.Close()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	service := New(config.Config{Services: []config.Service{{ID: "sonarr-1", Type: "sonarr", Name: "TV", URL: fake.URL, APIKey: "k"}}}, database)
	ctx := context.Background()
	err = service.VerifyUnmanagedContext(ctx, []string{filepath.Join(folder, "Season 01", "Show S01E01.mkv")})
	if err == nil || !strings.Contains(err.Error(), "claimed by Sonarr") {
		t.Errorf("the episode's file: %v", err)
	}
	if err := service.VerifyUnmanagedContext(ctx, []string{filepath.Join(folder, "Season 01", "extra.mkv")}); err != nil {
		t.Errorf("another file in its folder: %v", err)
	}
}
