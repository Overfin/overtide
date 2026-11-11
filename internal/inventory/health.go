package inventory

import (
	"context"
	"time"

	"github.com/overfin/overtide/internal/config"
)

// Each service's own state, as it says it (its version, its failing health
// checks), for the Services page and Home: asked of the service itself, not
// guessed from whether Overtide's last call to it went through.

// Service states.
const (
	StateRunning  = "running"  // it answers, with nothing failing
	StateWarnings = "warnings" // it answers, and says something is wrong
	StateDown     = "down"     // it doesn't answer
)

// ServiceHealth is a service's state when it was last asked.
type ServiceHealth struct {
	Version string
	State   string
	// Problems are what it says is wrong, in its words.
	Problems  []string
	CheckedAt time.Time
}

// Health is each service's state, by service ID, as last asked; a service
// not asked yet isn't there.
func (service *Service) Health() map[string]ServiceHealth {
	service.healthMu.RLock()
	defer service.healthMu.RUnlock()
	out := map[string]ServiceHealth{}
	for id, h := range service.health {
		out[id] = h
	}
	return out
}

// CheckHealth asks every enabled service its state.
func (service *Service) CheckHealth(ctx context.Context) {
	cfg := service.Config()
	out := map[string]ServiceHealth{}
	for _, svc := range cfg.Services {
		if !svc.Enabled() {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		h := service.healthOf(ctx, cfg, svc)
		cancel()
		h.CheckedAt = time.Now()
		out[svc.ID] = h
	}
	service.healthMu.Lock()
	service.health = out
	service.healthMu.Unlock()
}

func (service *Service) healthOf(ctx context.Context, cfg config.Config, svc config.Service) ServiceHealth {
	down := func(err error) ServiceHealth {
		return ServiceHealth{State: StateDown, Problems: []string{err.Error()}}
	}
	switch svc.Type {
	case "radarr", "sonarr":
		var version string
		var problems []string
		var err error
		if svc.Type == "radarr" {
			client, cerr := service.RadarrClient(svc.ID)
			if cerr != nil {
				return down(cerr)
			}
			v, p, e := client.WithContext(ctx).Health("v3")
			version, err = v, e
			for _, x := range p {
				problems = append(problems, x.Message)
			}
		} else {
			client, cerr := service.SonarrClient(svc.ID)
			if cerr != nil {
				return down(cerr)
			}
			v, p, e := client.WithContext(ctx).Health("v3")
			version, err = v, e
			for _, x := range p {
				problems = append(problems, x.Message)
			}
		}
		if err != nil {
			return down(err)
		}
		return withProblems(version, problems)
	case "prowlarr":
		client := service.ProwlarrClient()
		if client == nil {
			return ServiceHealth{State: StateDown}
		}
		version, p, err := client.WithContext(ctx).Health()
		if err != nil {
			return down(err)
		}
		var problems []string
		for _, x := range p {
			problems = append(problems, x.Message)
		}
		return withProblems(version, problems)
	case "qbittorrent":
		driver, err := service.qbittorrentClient(svc.ID)
		if err != nil {
			return down(err)
		}
		asker, ok := driver.(interface {
			Health() (string, string, error)
		})
		if !ok {
			return ServiceHealth{State: StateRunning}
		}
		if ctx != nil {
			if withContext, ok := driver.WithContext(ctx).(interface {
				Health() (string, string, error)
			}); ok {
				asker = withContext
			}
		}
		version, connection, err := asker.Health()
		if err != nil {
			return down(err)
		}
		switch connection {
		case "firewalled":
			return withProblems(version, []string{"Firewalled: other peers can't connect to it"})
		case "disconnected":
			return withProblems(version, []string{"Disconnected: no connection to the internet"})
		}
		return withProblems(version, nil)
	case "overfin":
		client := service.OverfinClient().WithContext(ctx)
		version, err := client.Server()
		if err != nil {
			// An Overfin older than /apps/v1/server: it runs if it answers.
			if verr := client.Validate(); verr != nil {
				return down(verr)
			}
			return withProblems("", nil)
		}
		return withProblems(version, nil)
	}
	return ServiceHealth{}
}

func withProblems(version string, problems []string) ServiceHealth {
	h := ServiceHealth{Version: version, State: StateRunning, Problems: problems}
	if len(problems) > 0 {
		h.State = StateWarnings
	}
	return h
}
