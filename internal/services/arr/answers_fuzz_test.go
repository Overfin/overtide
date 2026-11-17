package arr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// FuzzAnswers gives every call the same answer, whatever Radarr or Sonarr
// (or what poses as them) sends: none may panic, nor keep the client busy.
func FuzzAnswers(f *testing.F) {
	f.Add([]byte(`[{"id":1,"label":"overtide-keep"}]`))
	f.Add([]byte(`{"page":1,"pageSize":0,"totalRecords":5,"records":[{"downloadId":"ABC","id":3}]}`))
	f.Add([]byte(`{"page":2,"pageSize":10,"totalRecords":1000000,"records":[]}`))
	f.Add([]byte(`[{"path":"/data","accessible":true}]`))
	f.Add([]byte(`{"version":"5.0","status":"completed"}`))
	f.Add([]byte(`[{"source":"x","type":"error","message":"m"}]`))
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
		client := New("radarr", server.URL, "key").WithContext(ctx)
		client.Validate()
		labels, _ := client.TagLabels()
		Labels([]int{1, 2}, labels)
		client.EnsureTag("overtide-keep")
		client.Options()
		client.Destination()
		client.StorageRoots()
		client.CommandStatus(1)
		client.FindQueueItem("abc")
		client.ImportsSince(time.Time{}, func(HistoryRecord, time.Time) {})
		client.Health("v3")
		client.RecycleBin()
	})
}
