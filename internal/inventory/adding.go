package inventory

import (
	"fmt"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/prowlarr"
)

func buildProwlarrClient(configuration config.Config) *prowlarr.Client {
	if svc, ok := configuration.FirstService("prowlarr"); ok {
		return prowlarr.New(svc.URL, svc.APIKey)
	}
	return nil
}

// ProwlarrClient is the client of Prowlarr, for adding's pace; nil without
// Prowlarr.
func (service *Service) ProwlarrClient() *prowlarr.Client {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return service.prowlarr
}

// changeAdding applies a change to the settings that can be refused, and
// saves them. Without a config file (a Service built in tests) the change
// is only used.
func (service *Service) changeAdding(change func(config.Config) (config.Config, error)) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()
	service.mu.RLock()
	updated, err := change(service.cfg)
	path := service.configPath
	service.mu.RUnlock()
	if err != nil {
		return err
	}
	if path != "" {
		if err := config.Save(path, updated); err != nil {
			return fmt.Errorf("persist config: %w", err)
		}
	}
	service.mu.Lock()
	service.cfg = updated
	service.mu.Unlock()
	return nil
}

// SetAdding sets the adding settings together: all of them are saved, or
// none when one is wrong.
func (service *Service) SetAdding(mode, manage string, searches, titlesPerDay, upgradesPerDay int) error {
	return service.changeAdding(func(c config.Config) (config.Config, error) {
		c, err := config.SetAddingMode(c, mode)
		if err == nil {
			c, err = config.SetAddingManage(c, manage)
		}
		if err == nil {
			c, err = config.SetAddingSearches(c, searches)
		}
		if err == nil {
			c, err = config.SetTitlesPerDay(c, titlesPerDay)
		}
		if err == nil {
			c, err = config.SetUpgradesPerDay(c, upgradesPerDay)
		}
		return c, err
	})
}

// SetDestination adds a destination (index -1) or replaces one; the
// caller checked its folder and profile with the service.
func (service *Service) SetDestination(index int, d config.Destination) error {
	return service.changeAdding(func(c config.Config) (config.Config, error) { return config.SetDestination(c, index, d) })
}

// RemoveDestination takes a destination out.
func (service *Service) RemoveDestination(index int) error {
	return service.changeAdding(func(c config.Config) (config.Config, error) { return config.RemoveDestination(c, index) })
}
