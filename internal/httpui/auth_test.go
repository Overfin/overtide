package httpui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/store"
)

// newAuthTestServer builds a server backed by a real store and a real
// on-disk config file (SetCredentials requires a config path to persist
// to), so it exercises authGate the same way a real deployment would.
func newAuthTestServer(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, database)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), database
}

func postForm(t *testing.T, handler http.Handler, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func getPath(t *testing.T, handler http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func sessionCookieFrom(response *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	return nil
}

// send sends body as JSON, the way the panel does.
func send(t *testing.T, handler http.Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func post(t *testing.T, handler http.Handler, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return send(t, handler, http.MethodPost, path, body, cookie)
}

// setUp makes the admin account and returns its session.
func setUp(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	response := post(t, handler, "/api/setup", credentials{Username: "admin", Password: "correct-horse-battery", Confirm: "correct-horse-battery"}, nil)
	cookie := sessionCookieFrom(response)
	if response.Code != http.StatusOK || cookie == nil {
		t.Fatalf("setup: status=%d body=%q", response.Code, response.Body.String())
	}
	return cookie
}

func login(t *testing.T, handler http.Handler, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	return post(t, handler, "/api/login", credentials{Username: username, Password: password}, nil)
}

// decode reads a JSON answer.
func decode[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(response.Body.Bytes(), &v); err != nil {
		t.Fatalf("not JSON (%d): %q", response.Code, response.Body.String())
	}
	return v
}

func TestSessionSaysSetupFirst(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	state := decode[sessionState](t, getPath(t, handler, "/api/session", nil))
	if !state.NeedsSetup || state.SignedIn {
		t.Fatalf("%+v", state)
	}
	// The panel's page itself is open: it shows the setup.
	if page := getPath(t, handler, "/", nil); page.Code != http.StatusOK || !strings.Contains(strings.ToLower(page.Body.String()), "<!doctype html>") {
		t.Fatalf("page status=%d", page.Code)
	}
	for _, path := range []string{"/api/dashboard", "/ui/status"} {
		if response := getPath(t, handler, path, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s before setup: %d", path, response.Code)
		}
	}
}

func TestSetupRejectsMismatchedPasswords(t *testing.T) {
	handler, database := newAuthTestServer(t)
	response := post(t, handler, "/api/setup", credentials{Username: "admin", Password: "password1", Confirm: "password2"}, nil)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "do not match") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if sessionCookieFrom(response) != nil {
		t.Fatal("a mismatched setup must not issue a session")
	}
	if valid, _ := database.SessionValid("anything"); valid {
		t.Fatal("no session should exist yet")
	}
}

func TestSetupCreatesAccountAndSignsIn(t *testing.T) {
	handler, database := newAuthTestServer(t)
	cookie := setUp(t, handler)
	if valid, err := database.SessionValid(cookie.Value); err != nil || !valid {
		t.Fatalf("issued session is not valid: valid=%v err=%v", valid, err)
	}
	if state := decode[sessionState](t, getPath(t, handler, "/api/session", cookie)); !state.SignedIn || state.NeedsSetup || state.Username != "admin" {
		t.Fatalf("%+v", state)
	}
	// The account now exists: a second setup, signed in or not, must not
	// overwrite it (that would let anyone replace the admin account
	// without ever authenticating).
	for _, c := range []*http.Cookie{nil, cookie} {
		again := post(t, handler, "/api/setup", credentials{Username: "evil", Password: "another-password", Confirm: "another-password"}, c)
		if again.Code != http.StatusConflict || sessionCookieFrom(again) != nil {
			t.Fatalf("setup after account exists: status=%d", again.Code)
		}
	}
	if login(t, handler, "evil", "another-password").Code != http.StatusUnauthorized {
		t.Fatal("the second setup's account signs in")
	}
}

func TestAuthGateRequiresSessionOnceAccountExists(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	setUp(t, handler)
	if response := getPath(t, handler, "/api/dashboard", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: status=%d", response.Code)
	}
	loginResponse := login(t, handler, "admin", "correct-horse-battery")
	cookie := sessionCookieFrom(loginResponse)
	if loginResponse.Code != http.StatusOK || cookie == nil {
		t.Fatalf("login status=%d body=%q", loginResponse.Code, loginResponse.Body.String())
	}
	if authenticated := getPath(t, handler, "/api/dashboard", cookie); authenticated.Code != http.StatusOK {
		t.Fatalf("authenticated status=%d", authenticated.Code)
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	setUp(t, handler)
	response := login(t, handler, "admin", "wrong-password")
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "Incorrect username or password") {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
	if sessionCookieFrom(response) != nil {
		t.Fatal("a failed login must not issue a session")
	}
}

// A write must be JSON: a form another site's page posts is refused
// before anything is done.
func TestWritesMustBeJSON(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	response := postForm(t, handler, "/api/setup", url.Values{"username": {"admin"}, "password": {"correct-horse-battery"}, "confirm": {"correct-horse-battery"}}, nil)
	if response.Code != http.StatusUnsupportedMediaType || sessionCookieFrom(response) != nil {
		t.Fatalf("a form setup: status=%d", response.Code)
	}
	if state := decode[sessionState](t, getPath(t, handler, "/api/session", nil)); !state.NeedsSetup {
		t.Fatal("a form made the account")
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	handler, database := newAuthTestServer(t)
	cookie := setUp(t, handler)
	if response := post(t, handler, "/api/logout", struct{}{}, cookie); response.Code != http.StatusOK {
		t.Fatalf("logout status=%d", response.Code)
	}
	if valid, _ := database.SessionValid(cookie.Value); valid {
		t.Fatal("session must be deleted after logout")
	}
	if afterLogout := getPath(t, handler, "/api/dashboard", cookie); afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout status=%d", afterLogout.Code)
	}
}

// TestLoginLockoutAfterRepeatedFailures guards the escalating lockout added
// on top of bcrypt's own per-attempt cost: past a small threshold of failed
// attempts, a request is rejected before it ever reaches VerifyPassword —
// even when it now supplies the correct password.
func TestLoginLockoutAfterRepeatedFailures(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	setUp(t, handler)
	for i := 0; i < 5; i++ {
		if response := login(t, handler, "admin", "wrong"); !strings.Contains(response.Body.String(), "Incorrect username or password") {
			t.Fatalf("attempt %d body=%q", i, response.Body.String())
		}
	}
	lockedOut := login(t, handler, "admin", "correct-horse-battery")
	if lockedOut.Code != http.StatusTooManyRequests || !strings.Contains(lockedOut.Body.String(), "Too many attempts") {
		t.Fatalf("expected a lockout even with the correct password after repeated failures, got %d %q", lockedOut.Code, lockedOut.Body.String())
	}
	if sessionCookieFrom(lockedOut) != nil {
		t.Fatal("a locked-out attempt must not issue a session even with the right password")
	}
}

// TestLoginLockoutCountsWrongUsernameTheSameAsWrongPassword is a black-box
// proxy for the fix to a real timing side-channel: both a wrong username
// and a wrong password run the same password comparison and record the
// same failure, so a wrong username must count toward the same lockout.
func TestLoginLockoutCountsWrongUsernameTheSameAsWrongPassword(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	setUp(t, handler)
	for i := 0; i < 5; i++ {
		login(t, handler, "not-admin", "whatever")
	}
	if lockedOut := login(t, handler, "admin", "correct-horse-battery"); !strings.Contains(lockedOut.Body.String(), "Too many attempts") {
		t.Fatalf("wrong-username attempts should count toward the same lockout as wrong-password ones, got body=%q", lockedOut.Body.String())
	}
}

func TestHealthzBypassesAuthGate(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	if response := getPath(t, handler, "/healthz", nil); response.Code != http.StatusOK {
		t.Fatalf("/healthz status=%d", response.Code)
	}
}

// Without a database there are no sessions: the API answers nothing.
func TestAuthGateFailsClosedWithoutAStore(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/session", "/api/dashboard", "/ui/status"} {
		if response := getPath(t, server.Handler(), path, nil); response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status=%d", path, response.Code)
		}
	}
}

// The panel's built files are served as they are, any other address is
// its page, and an unknown API address is refused as JSON.
func TestPanelFiles(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	for _, path := range []string{"/", "/library", "/torrents/0123456789abcdef"} {
		if response := getPath(t, handler, path, nil); response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: %d %q", path, response.Code, response.Header().Get("Cache-Control"))
		}
	}
	session := setUp(t, handler)
	if response := getPath(t, handler, "/api/nothing", session); response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"error"`) {
		t.Errorf("unknown API: %d %q", response.Code, response.Body.String())
	}
}

// Every write with a body must be JSON, even without an Origin header (a
// client or extension that strips it): a plain-text or form POST to a
// route that changes something is refused before the handler runs.
func TestEveryWriteMustBeJSONWithoutOrigin(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	session := setUp(t, handler)
	for _, path := range []string{"/api/torrents-missing", "/api/refresh", "/api/logout", "/api/removal/execute", "/api/storage/plan/clean", "/api/storage/keep", "/api/tasks/run", "/api/services"} {
		for _, kind := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("x=1"))
			if kind != "" {
				request.Header.Set("Content-Type", kind)
			}
			request.AddCookie(session)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s as %q: %d, want 415", path, kind, response.Code)
			}
		}
	}
	if response := getPath(t, handler, "/api/dashboard", session); response.Code != http.StatusOK {
		t.Fatalf("the session ended: %d", response.Code)
	}
}
