package httpui

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// Setups sent all at once make one account: one of them signs in, and its
// password is the account's. None of the others gets a session.
func TestSetupMakesOneAccountWhenSentAtOnce(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	var mu sync.Mutex
	var signedIn []string
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			password := fmt.Sprintf("password-number-%02d", i)
			response := post(t, handler, "/api/setup", credentials{Username: "admin", Password: password, Confirm: password}, nil)
			if sessionCookieFrom(response) != nil {
				mu.Lock()
				signedIn = append(signedIn, password)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(signedIn) != 1 {
		t.Fatalf("%d setups signed in: %v", len(signedIn), signedIn)
	}
	response := login(t, handler, "admin", signedIn[0])
	if response.Code != http.StatusOK || sessionCookieFrom(response) == nil {
		t.Fatalf("the signed-in setup's password doesn't sign in: %d", response.Code)
	}
}
