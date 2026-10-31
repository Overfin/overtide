package httpui

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/inventory"
)

const (
	sessionCookieName = "overtide_session"
	// sessionDuration is deliberately long: Overtide is a single-user app
	// meant to stay signed in on the devices you actually use, not a
	// multi-tenant service where a short session limits blast radius.
	sessionDuration = 30 * 24 * time.Hour
)

// loginLimiter throttles repeated failed login attempts per client address.
// Bcrypt's own per-attempt cost already slows brute-forcing, but does
// nothing to stop a sustained scripted run over hours; this adds an
// escalating lockout on top of it. It is intentionally in-memory and
// per-process — Overtide has one admin account and one process, so nothing
// durable is lost by resetting on restart.
type loginLimiter struct {
	mu    sync.Mutex
	state map[string]*loginAttemptState
}

type loginAttemptState struct {
	failures    int
	lockedUntil time.Time
	last        time.Time
}

// loginLimiterSize is how many addresses the limiter holds before it
// forgets those that stopped trying loginForgetAfter ago.
const (
	loginLimiterSize = 1000
	loginForgetAfter = time.Hour
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{state: map[string]*loginAttemptState{}}
}

// attempt counts one try from key before its password is checked, and
// reports whether it may go ahead, or for how much longer key is locked
// out. Counting before checking holds the limit against tries sent all
// at once; recordSuccess clears it. Past a small threshold, each try
// starts an escalating lockout (15s per try past it, capped at 5 minutes)
// rather than an unlimited-attempt password oracle.
func (limiter *loginLimiter) attempt(key string) (time.Duration, bool) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	state := limiter.state[key]
	if state == nil {
		if len(limiter.state) >= loginLimiterSize {
			for k, old := range limiter.state {
				if now.Sub(old.last) > loginForgetAfter && !now.Before(old.lockedUntil) {
					delete(limiter.state, k)
				}
			}
		}
		// Still full (a burst from many addresses): the one heard from
		// longest ago makes room, one not locked out if there is one, so
		// the map never grows past its size.
		for len(limiter.state) >= loginLimiterSize {
			oldest, oldestLocked := "", ""
			for k, st := range limiter.state {
				if now.Before(st.lockedUntil) {
					if oldestLocked == "" || st.last.Before(limiter.state[oldestLocked].last) {
						oldestLocked = k
					}
				} else if oldest == "" || st.last.Before(limiter.state[oldest].last) {
					oldest = k
				}
			}
			if oldest == "" {
				oldest = oldestLocked
			}
			delete(limiter.state, oldest)
		}
		state = &loginAttemptState{}
		limiter.state[key] = state
	}
	if remaining := state.lockedUntil.Sub(now); remaining > 0 {
		return remaining, false
	}
	state.failures++
	state.last = now
	const freeAttempts = 4
	if state.failures > freeAttempts {
		backoff := time.Duration(state.failures-freeAttempts) * 15 * time.Second
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		state.lockedUntil = now.Add(backoff)
	}
	return 0, true
}

// recordSuccess clears key's failure history after a correct login.
func (limiter *loginLimiter) recordSuccess(key string) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	delete(limiter.state, key)
}

// clientLoginKey identifies the caller for login throttling. It strips the
// port from RemoteAddr since the same browser reconnects on a new one every
// request; behind a reverse proxy every client shares one address (and
// therefore one lockout bucket), which is an acceptable tradeoff for a
// single-admin app rather than trusting a spoofable forwarded-for header.
func clientLoginKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func newSessionToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

// requestIsHTTPS reports whether the browser's connection to Overtide (not
// necessarily this process's own listener) was HTTPS, so the session
// cookie's Secure flag reflects reality whether Overtide terminates TLS
// itself or sits behind a reverse proxy that does.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// startSession creates and persists a new session, then sets its cookie on
// the response. Used after both first-run setup and a normal login.
func (server *Server) startSession(w http.ResponseWriter, r *http.Request) error {
	token, err := newSessionToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(sessionDuration)
	if err := server.inv.Store().CreateSession(token, expires); err != nil {
		return err
	}
	setSessionCookie(w, r, token, expires)
	return nil
}

// authGate enforces Overtide's single-admin login in front of the API and
// the live updates. The panel's own files are open (they hold no data, and
// show the sign-in page); of the API, only what signing in needs is open.
// Without a database there are no sessions, so nothing else answers.
func (server *Server) authGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/ui/") {
			next.ServeHTTP(w, r)
			return
		}
		if server.inv == nil || server.inv.Store() == nil {
			fail(w, http.StatusServiceUnavailable, "Overtide has no database: sign-in is unavailable")
			return
		}
		switch r.URL.Path {
		case "/api/session", "/api/setup", "/api/login":
			next.ServeHTTP(w, r)
			return
		}
		if server.signedIn(r) {
			next.ServeHTTP(w, r)
			return
		}
		fail(w, http.StatusUnauthorized, "signed out")
	})
}

// signedIn reports whether r comes with a valid session.
func (server *Server) signedIn(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	valid, _ := server.inv.Store().SessionValid(cookie.Value)
	return valid
}

// sessionState is where the browser stands: the account to make first,
// or signed in or not.
type sessionState struct {
	NeedsSetup bool   `json:"needsSetup"`
	SignedIn   bool   `json:"signedIn"`
	Username   string `json:"username,omitempty"`
}

func (server *Server) session(w http.ResponseWriter, r *http.Request) {
	cfg := server.inv.Config()
	state := sessionState{NeedsSetup: cfg.Auth.Username == ""}
	if !state.NeedsSetup && server.signedIn(r) {
		state.SignedIn, state.Username = true, cfg.Auth.Username
	}
	writeJSON(w, http.StatusOK, state)
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Confirm is the password again, at setup.
	Confirm string `json:"confirm"`
}

// setup makes Overtide's one admin account. It works only while no account
// exists — once one does, it's refused instead of letting a second account
// overwrite the first.
func (server *Server) setup(w http.ResponseWriter, r *http.Request) {
	if server.inv.Config().Auth.Username != "" {
		fail(w, http.StatusConflict, "The account exists already: sign in.")
		return
	}
	var in credentials
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Password != in.Confirm {
		fail(w, http.StatusBadRequest, "Passwords do not match.")
		return
	}
	if err := server.inv.CreateAccount(strings.TrimSpace(in.Username), in.Password); errors.Is(err, inventory.ErrAccountExists) {
		fail(w, http.StatusConflict, "The account exists already: sign in.")
		return
	} else if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.startSession(w, r); err != nil {
		fail(w, http.StatusInternalServerError, "The account is made, but the session couldn't start: "+err.Error())
		return
	}
	done(w)
}

func (server *Server) login(w http.ResponseWriter, r *http.Request) {
	cfg := server.inv.Config()
	if cfg.Auth.Username == "" {
		fail(w, http.StatusConflict, "There is no account yet: set Overtide up.")
		return
	}
	var in credentials
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	key := clientLoginKey(r)
	if remaining, ok := server.loginLimiter.attempt(key); !ok {
		fail(w, http.StatusTooManyRequests, fmt.Sprintf("Too many attempts. Try again in %d seconds.", int(remaining.Seconds())+1))
		return
	}
	// Both checks always run, even when the username is already known to be
	// wrong: short-circuiting past VerifyPassword's bcrypt comparison would
	// make a wrong username return near-instantly while a right-username-
	// wrong-password takes bcrypt's cost — a timing side-channel that
	// discloses the admin username without ever guessing its password.
	usernameCorrect := strings.EqualFold(strings.TrimSpace(in.Username), cfg.Auth.Username)
	passwordCorrect := cfg.VerifyPassword(in.Password)
	if !usernameCorrect || !passwordCorrect {
		fail(w, http.StatusUnauthorized, "Incorrect username or password.")
		return
	}
	server.loginLimiter.recordSuccess(key)
	if err := server.startSession(w, r); err != nil {
		fail(w, http.StatusInternalServerError, "Signed in, but the session couldn't start: "+err.Error())
		return
	}
	done(w)
}

func (server *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = server.inv.Store().DeleteSession(cookie.Value)
	}
	clearSessionCookie(w, r)
	done(w)
}
