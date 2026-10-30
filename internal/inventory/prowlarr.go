package inventory

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/prowlarr"
	"github.com/overfin/overtide/internal/trackers"
)

// ProwlarrAddress is Prowlarr's address: the one saved, or else the one
// Radarr's or Sonarr's indexers point to ("" without either).
func (service *Service) ProwlarrAddress(ctx context.Context) string {
	cfg := service.Config()
	if cfg.Prowlarr.URL != "" {
		return cfg.Prowlarr.URL
	}
	for _, svc := range cfg.ServicesOfType("radarr") {
		if client, err := service.RadarrClient(svc.ID); err == nil {
			if address, err := client.WithContext(ctx).ProwlarrURL(); err == nil && address != "" && !strings.HasPrefix(address, cfg.Prowlarr.ProxyURL+config.ProxyPath) {
				return address
			}
		}
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		if client, err := service.SonarrClient(svc.ID); err == nil {
			if address, err := client.WithContext(ctx).ProwlarrURL(); err == nil && address != "" && !strings.HasPrefix(address, cfg.Prowlarr.ProxyURL+config.ProxyPath) {
				return address
			}
		}
	}
	return ""
}

// routedApps are the Prowlarr apps whose searches can go through Overtide.
func routedApps(apps []prowlarr.App) []prowlarr.App {
	var out []prowlarr.App
	for _, app := range apps {
		if kind := app.Implementation(); kind == "Radarr" || kind == "Sonarr" {
			out = append(out, app)
		}
	}
	return out
}

// SetProwlarr saves the connection to Prowlarr, and routes the searches of
// its apps (Radarr, Sonarr) through Overtide's proxy at proxyURL, or, route
// off, gives them back the address they had. An empty key keeps the one
// saved. Prowlarr tests each app as it's saved: one that can't reach the
// new address is refused, and the apps changed already are put back.
func (service *Service) SetProwlarr(ctx context.Context, address, key, proxyURL string, route bool) error {
	current := service.Config().Prowlarr
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	proxyURL = strings.TrimRight(strings.TrimSpace(proxyURL), "/")
	if key = strings.TrimSpace(key); key == "" {
		key = current.APIKey
	}
	if address == "" || key == "" {
		return fmt.Errorf("the address and API key of Prowlarr are needed")
	}
	if route && proxyURL == "" {
		return fmt.Errorf("an address for Overtide, as Radarr and Sonarr reach it, is needed")
	}
	client := prowlarr.New(address, key).WithContext(ctx)
	apps, err := client.Apps()
	if err != nil {
		return fmt.Errorf("no answer from Prowlarr: %w", err)
	}
	// Saved first: Prowlarr tests each app it saves through the new
	// address, so the proxy must answer already.
	if err := service.changeConfig(func(c config.Config) config.Config {
		c.Prowlarr.URL, c.Prowlarr.APIKey, c.Prowlarr.ProxyURL = address, key, proxyURL
		return c
	}); err != nil {
		return err
	}
	replaced := map[string]string{}
	for id, old := range current.Replaced {
		replaced[id] = old
	}
	var changed []prowlarr.App
	putBack := func() {
		for _, app := range changed {
			if old, ok := replaced[strconv.Itoa(app.ID())]; ok {
				app.SetField("prowlarrUrl", old)
				_ = client.UpdateApp(app)
			}
		}
	}
	for _, app := range routedApps(apps) {
		id := strconv.Itoa(app.ID())
		var want string
		if route {
			want = proxyURL + config.ProxyPath
			if _, saved := replaced[id]; !saved {
				replaced[id] = app.Field("prowlarrUrl")
			}
		} else {
			old, saved := replaced[id]
			if !saved {
				continue
			}
			want = old
		}
		if app.Field("prowlarrUrl") == want {
			continue
		}
		app.SetField("prowlarrUrl", want)
		if err := client.UpdateApp(app); err != nil {
			if route {
				putBack()
			}
			return fmt.Errorf("%s: %w", app.Name(), err)
		}
		changed = append(changed, app)
	}
	if len(changed) > 0 {
		if err := client.SyncApps(); err != nil {
			return fmt.Errorf("the apps changed in Prowlarr, but aren't synced yet: %w", err)
		}
	}
	if !route {
		replaced = nil
	}
	return service.changeConfig(func(c config.Config) config.Config {
		c.Prowlarr = config.ProwlarrConfig{URL: address, APIKey: key, ProxyURL: proxyURL, Route: route, Replaced: replaced}
		return c
	})
}

// TrackerLimits are the budgets of Prowlarr's trackers, by their ID there.
func (service *Service) TrackerLimits() (map[int]trackers.Limit, error) {
	cfg := service.Config().Prowlarr
	if cfg.URL == "" || cfg.APIKey == "" {
		return nil, nil
	}
	indexers, err := prowlarr.New(cfg.URL, cfg.APIKey).Indexers()
	if err != nil {
		return nil, err
	}
	limits := make(map[int]trackers.Limit, len(indexers))
	for _, indexer := range indexers {
		limits[indexer.ID] = trackers.Limit{Name: indexer.Name, Queries: indexer.QueryLimit, Grabs: indexer.GrabLimit, Unit: indexer.LimitsUnit}
	}
	return limits, nil
}
