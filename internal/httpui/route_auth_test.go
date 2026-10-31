package httpui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every route registered in the source answers 401 to a browser that isn't
// signed in, except what signing in itself needs, the panel's own files and
// the health check. The routes are read from the source, so a route added
// later is checked without anyone listing it here.
func TestEveryRouteNeedsASession(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	setUp(t, handler) // an account exists: nothing is open for setup any more
	open := map[string]bool{"/": true, "/healthz": true, "GET /api/session": true, "POST /api/setup": true, "POST /api/login": true}
	files, _ := filepath.Glob("*.go")
	pattern := regexp.MustCompile(`mux\.HandleFunc\("([^"]+)"`)
	param := regexp.MustCompile(`\{[^}]+\}`)
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(source), -1) {
			route := match[1]
			if open[route] {
				continue
			}
			method, path := http.MethodGet, route
			if before, after, ok := strings.Cut(route, " "); ok {
				method, path = before, after
			}
			path = param.ReplaceAllString(path, "x")
			request := httptest.NewRequest(method, path, strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Errorf("%s (%s %s) signed out: %d, want 401", route, method, path, response.Code)
			}
			checked++
		}
	}
	if checked < 30 {
		t.Fatalf("only %d routes found in the source: the pattern no longer matches how routes are registered", checked)
	}
	// Overswell's room check is open to its key only.
	if response := getPath(t, handler, roomPath, nil); response.Code != http.StatusUnauthorized {
		t.Errorf("%s without a key: %d", roomPath, response.Code)
	}
}
