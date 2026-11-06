package inventory

import (
	"context"
	"fmt"
	"slices"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/arr"
	"github.com/overfin/overtide/internal/services/prowlarr"
)

func buildProwlarrClient(configuration config.Config) *prowlarr.Client {
	if svc, ok := configuration.FirstService("prowlarr"); ok {
		return prowlarr.New(svc.URL, svc.APIKey)
	}
	return nil
}

// checkProwlarr records whether the Prowlarr answers, for the Services
// page. Only adding's pace depends on it, so it never stops an inventory.
func (service *Service) checkProwlarr(ctx context.Context) {
	svc, ok := service.Config().FirstService("prowlarr")
	client := service.ProwlarrClient()
	if !ok || client == nil {
		return
	}
	err := client.WithContext(ctx).Validate()
	service.setStatus(svc.Name, true, err == nil, err)
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

// DestinationOptions are a Radarr's or a Sonarr's root folders and
// quality profiles.
type DestinationOptions struct {
	Folders  []string
	Profiles []arr.QualityProfile
}

// Options asks a Radarr or Sonarr for its root folders and quality
// profiles.
func (service *Service) Options(ctx context.Context, serviceID string) (DestinationOptions, error) {
	var client *arr.Client
	if r, err := service.RadarrClient(serviceID); err == nil {
		client = r.Client
	} else if s, err := service.SonarrClient(serviceID); err == nil {
		client = s.Client
	} else {
		return DestinationOptions{}, fmt.Errorf("no Radarr or Sonarr %q", serviceID)
	}
	folders, profiles, err := client.WithContext(ctx).Options()
	return DestinationOptions{Folders: folders, Profiles: profiles}, err
}

// SetDestination adds a destination (index -1) or replaces one, once its
// service says it has that root folder and quality profile.
func (service *Service) SetDestination(ctx context.Context, index int, d config.Destination) error {
	options, err := service.Options(ctx, d.ServiceID)
	if err != nil {
		return err
	}
	if !slices.Contains(options.Folders, d.RootFolder) {
		return fmt.Errorf("that service has no root folder %q", d.RootFolder)
	}
	if !slices.ContainsFunc(options.Profiles, func(p arr.QualityProfile) bool { return p.ID == d.QualityProfileID }) {
		return fmt.Errorf("that service has no quality profile %d", d.QualityProfileID)
	}
	return service.changeAdding(func(c config.Config) (config.Config, error) { return config.SetDestination(c, index, d) })
}

// RemoveDestination takes a destination out.
func (service *Service) RemoveDestination(index int) error {
	return service.changeAdding(func(c config.Config) (config.Config, error) { return config.RemoveDestination(c, index) })
}
