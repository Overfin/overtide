package httpui

import (
	"strings"
	"sync"
	"testing"
)

// Wrong passwords sent all at once from one address get no more tries
// than sent one after the other: the four free ones and the one that
// starts the lockout.
func TestLoginLimitHoldsAgainstParallelTries(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	setUp(t, handler)

	var mu sync.Mutex
	checked, refused := 0, 0
	var wg sync.WaitGroup
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := login(t, handler, "admin", "wrong-password").Body.String()
			mu.Lock()
			defer mu.Unlock()
			switch {
			case strings.Contains(body, "Incorrect username or password"):
				checked++
			case strings.Contains(body, "Too many attempts"):
				refused++
			}
		}()
	}
	wg.Wait()
	if checked != 5 || refused != 35 {
		t.Fatalf("%d passwords checked, %d refused; want 5 and 35", checked, refused)
	}
}

// The limiter forgets addresses that stopped trying, so it doesn't grow
// with every address that ever tried.
func TestLoginLimiterForgetsOldAddresses(t *testing.T) {
	limiter := newLoginLimiter()
	for i := range loginLimiterSize + 10 {
		limiter.attempt("10.0.0." + strings.Repeat("x", i))
	}
	limiter.mu.Lock()
	for _, state := range limiter.state {
		state.last = state.last.Add(-2 * loginForgetAfter)
	}
	limiter.mu.Unlock()
	limiter.attempt("new")
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.state) != 1 {
		t.Fatalf("%d addresses kept, want only the new one", len(limiter.state))
	}
}
