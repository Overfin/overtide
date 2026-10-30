package httpui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
)

// The room check answers only with Overtide's API key, never a session or
// nothing; a folder on no disk Overtide knows is "unknown".
func TestRoomNeedsTheAPIKey(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(inventory.New(cfg, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	ask := func(key, query string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/room"+query, nil)
		if key != "" {
			request.Header.Set("X-Api-Key", key)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, key := range []string{"", "wrong"} {
		if response := ask(key, "?folder=/data/Films"); response.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: %d", key, response.Code)
		}
	}
	if response := ask(cfg.APIKey, ""); response.Code != http.StatusBadRequest {
		t.Fatalf("no folder: %d", response.Code)
	}
	response := ask(cfg.APIKey, "?folder=/data/Films")
	var answer roomAnswer
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &answer) != nil || answer.State != inventory.RoomUnknown {
		t.Fatalf("unknown folder: %d %s", response.Code, response.Body)
	}
}
