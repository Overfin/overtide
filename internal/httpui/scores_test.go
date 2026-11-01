package httpui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/tasks"
)

// Opening a cleanup plan reads Overfin's scores first and waits for them;
// skipping a row in the open window doesn't read them again.
func TestCleanupPlanReadsScoresFirst(t *testing.T) {
	var reads atomic.Int32
	manager := tasks.New(tasks.Definition{ID: "overfin", Name: "Overfin values", Runner: func(context.Context) error {
		reads.Add(1)
		return nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Start(ctx)
	cfg := config.Config{Overfin: config.Connection{URL: "http://192.0.2.10:8080", APIKey: "k"}}
	server := &Server{tasks: manager, inv: inventory.New(cfg, nil)}
	handler := server.routes()

	get := func(target string) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d", target, recorder.Code)
		}
	}
	get("/api/storage/plan?path=/data/movies")
	if reads.Load() != 1 {
		t.Fatalf("scores read %d times on opening, want 1", reads.Load())
	}
	get("/api/storage/plan?path=/data/movies&excluded=torrent:abc")
	if reads.Load() != 1 {
		t.Fatalf("scores read again on a skip: %d", reads.Load())
	}
}
