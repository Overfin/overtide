package tasks

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

// Many tasks with shared and exclusive claims on a few resources, submitted
// at random from several goroutines: an exclusive claim never runs beside
// any other claim on its resource, shared ones may overlap, and every
// trigger finishes.
func TestStressResourceSafety(t *testing.T) {
	resources := []string{"a", "b", "c"}
	var mu sync.Mutex
	holders := map[string]map[string]ClaimMode{} // resource → task → mode
	violations := []string{}
	definitions := []Definition{}
	for i := range 12 {
		id := fmt.Sprintf("task-%d", i)
		claims := []ResourceClaim{}
		for _, resource := range resources {
			switch rand.IntN(3) {
			case 1:
				claims = append(claims, ResourceClaim{Resource: resource, Mode: ClaimShared})
			case 2:
				claims = append(claims, ResourceClaim{Resource: resource, Mode: ClaimExclusive})
			}
		}
		priority := []Priority{PriorityPeriodic, PriorityManual, PriorityMutation}[rand.IntN(3)]
		definitions = append(definitions, Definition{ID: id, Name: id, Resources: claims, Priority: priority, Runner: func(ctx context.Context) error {
			mu.Lock()
			for _, claim := range claims {
				if holders[claim.Resource] == nil {
					holders[claim.Resource] = map[string]ClaimMode{}
				}
				for other, mode := range holders[claim.Resource] {
					if claim.Mode == ClaimExclusive || mode == ClaimExclusive {
						violations = append(violations, fmt.Sprintf("%s (%s) beside %s (%s) on %s", id, claim.Mode, other, mode, claim.Resource))
					}
				}
				holders[claim.Resource][id] = claim.Mode
			}
			mu.Unlock()
			time.Sleep(time.Duration(rand.IntN(3)) * time.Millisecond)
			mu.Lock()
			for _, claim := range claims {
				delete(holders[claim.Resource], id)
			}
			mu.Unlock()
			if rand.IntN(10) == 0 {
				return Retryable(fmt.Errorf("passing"))
			}
			return nil
		}, Retry: RetryPolicy{MaxAttempts: 2, InitialDelay: time.Millisecond}})
	}
	manager := New(definitions...)
	startManager(t, manager)
	var wg sync.WaitGroup
	triggers := make(chan string, 2000)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				task := definitions[rand.IntN(len(definitions))].ID
				receipt, err := manager.Submit(Request{TaskID: task, Durable: rand.IntN(2) == 0, CoalescingKey: fmt.Sprint(rand.IntN(3))})
				if err != nil {
					t.Error(err)
					return
				}
				triggers <- receipt.TriggerID
			}
		}()
	}
	wg.Wait()
	close(triggers)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for id := range triggers {
		if _, err := manager.Await(ctx, id); err != nil && ctx.Err() != nil {
			t.Fatalf("trigger %s never finished", id)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(violations) > 0 {
		t.Fatalf("%d resource violations, first: %s", len(violations), violations[0])
	}
}

// Stopped in the middle of its work and made again from its saved state,
// the scheduler still runs every durable trigger to its end: none lost,
// none left pending.
func TestStressRestartKeepsDurableWork(t *testing.T) {
	store := &memoryStateStore{}
	define := func() []Definition {
		out := []Definition{}
		for i := range 4 {
			id := fmt.Sprintf("task-%d", i)
			out = append(out, Definition{ID: id, Name: id, Interruptible: i%2 == 0,
				Resources: []ResourceClaim{{Resource: "r", Mode: []ClaimMode{ClaimShared, ClaimExclusive}[i%2]}},
				Runner: func(ctx context.Context) error {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Duration(rand.IntN(5)) * time.Millisecond):
					}
					return nil
				}})
		}
		return out
	}
	durable := []string{}
	for round := range 5 {
		manager, err := newManager(store, realClock{}, define()...)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go manager.Start(ctx)
		for range 30 {
			receipt, err := manager.Submit(Request{TaskID: fmt.Sprintf("task-%d", rand.IntN(4)), Durable: true, CoalescingKey: fmt.Sprint(rand.IntN(5))})
			if err != nil {
				t.Fatal(err)
			}
			durable = append(durable, receipt.TriggerID)
		}
		time.Sleep(time.Duration(rand.IntN(10)) * time.Millisecond)
		cancel()
		select {
		case <-manager.Done():
		case <-time.After(20 * time.Second):
			t.Fatalf("round %d: the scheduler didn't stop", round)
		}
	}
	manager, err := newManager(store, realClock{}, define()...)
	if err != nil {
		t.Fatal(err)
	}
	startManager(t, manager)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, id := range durable {
		result, err := manager.Await(ctx, id)
		if ctx.Err() != nil {
			t.Fatalf("durable trigger %s never finished", id)
		}
		// Interrupted non-interruptible work needs attention: that's an end too.
		if err != nil && result.State != "attention" {
			t.Errorf("trigger %s: %s %v", id, result.State, err)
		}
	}
}
