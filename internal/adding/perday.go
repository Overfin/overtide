package adding

import (
	"encoding/json"
	"fmt"
	"time"
)

// startedKey keeps when each title started downloading over the last 24
// hours (a series, once per season): the daily limit.
const startedKey = "adding.started"

// started returns when titles started downloading over the last 24 hours,
// oldest first.
func (service *Service) started() ([]time.Time, error) {
	return service.lastDay(startedKey)
}

// lastDay returns the times kept under key, those of the last 24 hours,
// oldest first.
func (service *Service) lastDay(key string) ([]time.Time, error) {
	raw, err := service.inv.Store().Meta(key)
	if err != nil || raw == "" {
		return nil, err
	}
	var times []time.Time
	if err := json.Unmarshal([]byte(raw), &times); err != nil {
		return nil, fmt.Errorf("read the downloads started: %w", err)
	}
	since := service.now().Add(-24 * time.Hour)
	kept := times[:0]
	for _, t := range times {
		if t.After(since) {
			kept = append(kept, t)
		}
	}
	return kept, nil
}

// recordStarted counts n downloads started now.
func (service *Service) recordStarted(n int) error {
	return service.recordNow(startedKey, n)
}

// recordNow adds n times to those kept under key.
func (service *Service) recordNow(key string, n int) error {
	times, err := service.lastDay(key)
	if err != nil {
		return err
	}
	for range n {
		times = append(times, service.now())
	}
	encoded, err := json.Marshal(times)
	if err != nil {
		return err
	}
	return service.inv.Store().SetMeta(key, string(encoded))
}

// DailyLimit says whether n more titles may start downloading now, under
// the titles a day; if not, until when.
func (service *Service) DailyLimit(n int) (bool, time.Time) {
	times, err := service.started()
	if err != nil {
		return false, time.Time{}
	}
	limit := service.inv.Config().Adding.PerDay()
	if len(times)+n <= limit || len(times) == 0 {
		return true, time.Time{}
	}
	// Room comes back as the oldest ones turn 24 hours old.
	over := len(times) + n - limit
	if over > len(times) {
		over = len(times)
	}
	return false, times[over-1].Add(24 * time.Hour)
}
