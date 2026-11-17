package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// FuzzAnswers gives every call the same answer, whatever qBittorrent (or
// what poses as it) sends: none may panic.
func FuzzAnswers(f *testing.F) {
	f.Add([]byte(`[{"hash":"abc","name":"x","save_path":"/d","content_path":"/d/x","progress":1}]`))
	f.Add([]byte(`{"rid":2,"full_update":true,"torrents":{"abc":{"name":"x","save_path":"/d"}},"torrents_removed":["def"]}`))
	f.Add([]byte(`[{"index":0,"name":"../../etc/passwd","size":-1,"progress":2}]`))
	f.Add([]byte(`{"save_path":"/d","temp_path":"/t","temp_path_enabled":true}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, body []byte) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v2/auth/login" {
				http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s"})
				w.Write([]byte("Ok."))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client := New("", server.URL, "admin", "secret", "").WithContext(ctx)
		start := time.Now()
		defer func() {
			if time.Since(start) > 4*time.Second {
				t.Fatalf("an answer kept the client busy %s", time.Since(start))
			}
		}()
		torrents, _ := client.Inventory()
		client.Sync(nil, 0)
		client.Sync(torrents, 3)
		client.Detail("abc")
		client.AllFiles(torrents)
		client.ClaimedFiles(torrents)
		client.StorageRoots(torrents)
		client.ClaimedByOthers([]string{"/d/x"}, map[string]bool{"abc": true})
		client.Health()
	})
}
