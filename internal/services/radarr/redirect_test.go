package radarr

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A Radarr behind something that redirects elsewhere (a sign-in page, say)
// doesn't get a file "deleted": Go would follow the redirect as a GET, with
// the API key, and take the sign-in page's 200 for a deletion.
func TestDeleteFileRedirectedElsewhereIsAnError(t *testing.T) {
	var reached []string
	signIn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.Method+" key="+r.Header.Get("X-Api-Key"))
	}))
	defer signIn.Close()
	elsewhere := "http://localhost" + signIn.URL[strings.LastIndex(signIn.URL, ":"):]
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere+"/login", http.StatusFound)
	}))
	defer radarr.Close()
	if err := New(radarr.URL, "secret").DeleteFile(10); err == nil {
		t.Fatal("a redirected delete counted as done")
	}
	if len(reached) != 0 {
		t.Fatalf("the other host was asked: %v", reached)
	}
}
