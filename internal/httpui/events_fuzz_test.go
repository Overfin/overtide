package httpui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/pacing"
	"github.com/overfin/overtide/internal/services/prowlarr"
)

// FuzzArrEvents sends Radarr's and Sonarr's event route anything, signed
// or not: it never panics, lets nothing in without the secret, and answers
// a signed body as an event or as not one.
func FuzzArrEvents(f *testing.F) {
	f.Add(`{"eventType":"MovieAdded","movie":{"id":1}}`, true)
	f.Add(`{"eventType":"Test"}`, false)
	f.Add(`{"eventType":null}`, true)
	f.Add(`[`, true)
	server, handler := libraryServer(f, config.Config{})
	adder := adding.New(server.inv)
	server.SetAdding(adder, pacing.New(func() *prowlarr.Client { return nil }))
	secret, err := adder.EventsSecret()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, body string, signed bool) {
		request := httptest.NewRequest(http.MethodPost, adding.EventsPath, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		password := "wrong"
		if signed {
			password = secret
		}
		request.SetBasicAuth(adding.EventsUser, password)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		switch {
		case !signed && response.Code != http.StatusUnauthorized:
			t.Fatalf("unsigned: %d", response.Code)
		case signed && response.Code != http.StatusNoContent && response.Code != http.StatusBadRequest:
			t.Fatalf("signed: %d", response.Code)
		}
	})
}
