package httpui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetupNeedsCodeOnlyUnderAName(t *testing.T) {
	for host, want := range map[string]bool{
		"192.0.2.1:8088":        false,
		"192.0.2.1":             false,
		"[2001:db8::1]:8088":    false,
		"localhost:8088":        false,
		"LOCALHOST":             false,
		"overtide.example.org":  true,
		"example.org:8088":      true,
		"localhost.example.org": true,
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/setup", nil)
		r.Host = host
		if got := setupNeedsCode(r); got != want {
			t.Errorf("%s: needs code %v, want %v", host, got, want)
		}
	}
}

// Opened by a name (a page's own name pointed at Overtide, DNS
// rebinding), setup makes no account without the code from the log.
func TestSetupByNameNeedsTheCode(t *testing.T) {
	_, server := newAuthTestServerWith(t)
	handler := server.Handler()
	setupByName := func(code string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(credentials{Username: "admin", Password: "correct-horse-battery", Confirm: "correct-horse-battery", Code: code})
		request := httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewReader(raw))
		request.Host = "rebound.example.org"
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	session := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	session.Host = "rebound.example.org"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, session)
	if state := decode[sessionState](t, response); !state.NeedsSetup || !state.SetupCode {
		t.Fatalf("session by name: %+v", state)
	}
	for _, code := range []string{"", "WRONGCODE123"} {
		if response := setupByName(code); response.Code != http.StatusForbidden || sessionCookieFrom(response) != nil {
			t.Fatalf("code %q: status %d", code, response.Code)
		}
	}
	if server.inv.Config().Auth.Username != "" {
		t.Fatal("an account was made without the code")
	}
	code, err := server.currentSetupCode()
	if err != nil {
		t.Fatal(err)
	}
	if response := setupByName(" " + code + " "); response.Code != http.StatusOK || sessionCookieFrom(response) == nil {
		t.Fatalf("with the code: status %d %s", response.Code, response.Body.String())
	}
}
