package overfin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// FuzzAnswers gives every call the same answer, whatever Overfin (or what
// poses as it) sends: none may panic, nor keep the client busy.
func FuzzAnswers(f *testing.F) {
	f.Add([]byte(`{"titles":[{"tmdb":1,"kind":"movie","value":0.5,"seasons":[{"number":1,"value":0.2}]}],"next":"x"}`))
	f.Add([]byte(`{"titles":[{"tmdb":-1,"kind":"series","seasons":null}],"next":""}`))
	f.Add([]byte(`{"events":[{"tmdb":1,"at":"2026-11-01T00:00:00Z"}]}`))
	f.Add([]byte(`{"categories":[{"id":"films","name":"Films"}]}`))
	f.Add([]byte(`{"seasons":[{"number":1,"orders":[{"name":"dvd","episodes":[1,2]}]}]}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, body []byte) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start := time.Now()
		defer func() {
			if time.Since(start) > 4*time.Second {
				t.Fatalf("an answer kept the client busy %s", time.Since(start))
			}
		}()
		client := New(server.URL, "key").WithContext(ctx)
		client.Validate()
		client.Fetch()
		client.LibraryValues()
		client.Suggestions(50)
		client.LeftSince(time.Now().Add(-time.Hour))
		client.Categories()
		client.Wishes(50)
		client.EpisodeOrders(1)
		client.Server()
		client.Measures()
		client.Departures(nil)
	})
}
