package tries

import (
	"context"
	"testing"
	"time"
)

func TestRetryDelay(t *testing.T) {
	want := []int{1, 2, 4, 8, 16, 30, 30}
	for i, days := range want {
		if got := RetryDelay(i + 1); got != time.Duration(days)*24*time.Hour {
			t.Errorf("try %d: %s, want %d days", i+1, got, days)
		}
	}
}

func TestSearchAndCheck(t *testing.T) {
	PollEvery = time.Millisecond
	statuses := []string{"queued", "started", "completed"}
	status := func(int) (string, error) {
		next := statuses[0]
		if len(statuses) > 1 {
			statuses = statuses[1:]
		}
		return next, nil
	}
	grabbed, err := SearchAndCheck(context.Background(), func() (int, error) { return 7, nil }, status, func() (bool, error) { return true, nil })
	if err != nil || !grabbed {
		t.Fatalf("got %v %v", grabbed, err)
	}
	failed := func(int) (string, error) { return "failed", nil }
	if _, err := SearchAndCheck(context.Background(), func() (int, error) { return 7, nil }, failed, func() (bool, error) { return true, nil }); err == nil {
		t.Fatal("a failed search went through")
	}
}
