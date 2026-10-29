package tasks

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSuccessorWaitsForTheRunningTask(t *testing.T) {
	started := make(chan int, 2)
	release := make(chan struct{}, 2)
	var n atomic.Int32
	manager := New(Definition{ID: "inventory", Name: "Inventory", Runner: func(context.Context) error {
		started <- int(n.Add(1))
		<-release
		return nil
	}})
	startManager(t, manager)
	first, _ := manager.Submit(Request{TaskID: "inventory", Kind: TriggerEvent, Durable: true, RequiredCoverage: 1})
	receive(t, started)
	second, _ := manager.Submit(Request{TaskID: "inventory", Kind: TriggerEvent, Durable: true, RequiredCoverage: 2})
	select {
	case got := <-started:
		t.Fatalf("run %d started while run 1 was still running", got)
	case <-time.After(300 * time.Millisecond):
	}
	release <- struct{}{}
	release <- struct{}{}
	_, _ = manager.Await(context.Background(), first.TriggerID)
	_, _ = manager.Await(context.Background(), second.TriggerID)
}
