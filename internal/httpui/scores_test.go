package httpui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// Overfin's read waiting out its cooldown after a removal interrupted it
// (half an hour) isn't waited for: the plan opens at once, on the scores
// as they are, and says so.
func TestCleanupPlanDoesntWaitForTheCooldown(t *testing.T) {
	manager := tasks.New(tasks.Definition{ID: "overfin", Name: "Overfin values", Coalesce: true, Runner: func(context.Context) error { return nil }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go manager.Start(ctx)
	// As a removal leaves it: queued again for after the cooldown.
	if _, err := manager.Submit(tasks.Request{TaskID: "overfin", Kind: tasks.TriggerEvent, NotBefore: time.Now().Add(30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Overfin: config.Connection{URL: "http://192.0.2.10:8080", APIKey: "k"}}
	server := &Server{tasks: manager, inv: inventory.New(cfg, nil)}
	handler := server.routes()
	start := time.Now()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/storage/plan?path=/data/movies", nil))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the plan waited %s for a read due in half an hour", took)
	}
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"oldScores":true`) {
		t.Fatalf("%d %s", recorder.Code, recorder.Body)
	}
}
