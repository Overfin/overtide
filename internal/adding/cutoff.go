package adding

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/sonarr"
)

const (
	// cutoffKey keeps what Radarr and Sonarr listed below their cutoff.
	cutoffKey = "adding.below-cutoff"
	// cutoffFresh is how long that list is used before it's read again:
	// Sonarr lists each episode, and reading them all takes a while (17,000
	// episodes: a minute and a half).
	cutoffFresh = 30 * time.Minute
)

// belowCutoff is what Radarr and Sonarr listed below their quality cutoff,
// by service, and when.
type belowCutoff struct {
	At      time.Time                     `json:"at"`
	Films   map[string][]int              `json:"films"`
	Seasons map[string][]sonarr.SeasonRef `json:"seasons"`
}

// cutoffState is the kept list and whether a read of it is going on.
type cutoffState struct {
	mu      sync.Mutex
	list    *belowCutoff
	reading bool
	// done lets tests wait for a read behind the page.
	done sync.WaitGroup
}

// belowCutoff returns what's below cutoff, read again when older than
// cutoffFresh. wait: read it now if it's stale (a run, in the background);
// else the kept list comes back at once and a fresh one is read behind it.
// With none kept yet (the first time ever: it's kept in the database), it's
// read now either way.
func (service *Service) belowCutoff(ctx context.Context, cfg config.Config, wait bool) (list belowCutoff, ok bool, err error) {
	state := &service.cutoff
	state.mu.Lock()
	if state.list == nil {
		if raw, err := service.inv.Store().Meta(cutoffKey); err == nil && raw != "" {
			var kept belowCutoff
			if json.Unmarshal([]byte(raw), &kept) == nil {
				state.list = &kept
			}
		}
	}
	kept := state.list
	fresh := kept != nil && service.now().Sub(kept.At) < cutoffFresh
	if fresh {
		state.mu.Unlock()
		return *kept, true, nil
	}
	if wait || kept == nil {
		state.mu.Unlock()
		read, err := service.readCutoff(ctx, cfg)
		if err != nil {
			return belowCutoff{}, false, err
		}
		return read, true, nil
	}
	if !state.reading {
		state.reading = true
		state.done.Add(1)
		go func() {
			defer state.done.Done()
			readCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			defer cancel()
			if _, err := service.readCutoff(readCtx, cfg); err != nil {
				log.Printf("[adding] what's below cutoff: %v", err)
			}
			state.mu.Lock()
			state.reading = false
			state.mu.Unlock()
		}()
	}
	state.mu.Unlock()
	return *kept, true, nil
}

// CutoffAt is when the kept list of what's below cutoff was read, and
// whether a fresh one is being read now.
func (service *Service) CutoffAt() (at time.Time, reading bool) {
	state := &service.cutoff
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.list != nil {
		at = state.list.At
	}
	return at, state.reading
}

// readCutoff asks every Radarr and Sonarr what's below cutoff, and keeps it.
func (service *Service) readCutoff(ctx context.Context, cfg config.Config) (belowCutoff, error) {
	read := belowCutoff{At: service.now(), Films: map[string][]int{}, Seasons: map[string][]sonarr.SeasonRef{}}
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			return read, err
		}
		if read.Films[svc.ID], err = client.WithContext(ctx).BelowCutoff(); err != nil {
			return read, err
		}
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return read, err
		}
		if read.Seasons[svc.ID], err = client.WithContext(ctx).BelowCutoff(); err != nil {
			return read, err
		}
	}
	encoded, err := json.Marshal(read)
	if err != nil {
		return read, err
	}
	if err := service.inv.Store().SetMeta(cutoffKey, string(encoded)); err != nil {
		return read, err
	}
	state := &service.cutoff
	state.mu.Lock()
	state.list = &read
	state.mu.Unlock()
	return read, nil
}
