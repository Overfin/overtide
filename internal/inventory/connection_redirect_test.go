package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/config"
)

// Adding a service whose address redirects elsewhere (a sign-in page, say)
// fails with the reason, for the operator to give its direct address.
func TestConnectionRedirectedElsewhereSaysWhy(t *testing.T) {
	signIn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the sign-in host was asked %s", r.URL.Path)
	}))
	defer signIn.Close()
	elsewhere := "http://localhost" + signIn.URL[strings.LastIndex(signIn.URL, ":"):]
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere+"/login", http.StatusFound)
	}))
	defer service.Close()
	for _, kind := range []string{"radarr", "sonarr", "overfin", "qbittorrent"} {
		svc := config.Service{Type: kind, Name: kind, URL: service.URL, APIKey: "k"}
		if kind == "qbittorrent" {
			svc.APIKey, svc.Username, svc.Password = "", "u", "p"
		}
		err := validateServiceConnection(context.Background(), svc)
		if err == nil || !strings.Contains(err.Error(), "another host or port") {
			t.Errorf("%s: %v", kind, err)
		}
	}
}
