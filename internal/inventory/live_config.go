package inventory

import (
	"context"
	"fmt"
	"log"
	"maps"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/qbittorrent"
	"github.com/overfin/overtide/internal/services/radarr"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/valuation"
)

// AddService is the first slice of live config editing: validate a new
// service (structurally, then a real connection check), persist it, and
// activate its client immediately — all without disturbing the Service's
// existing in-memory reconciliation state (items, torrents, files, generation
// counters). Only *adding* a not-yet-configured service type is
// supported today; the runtime still has one adapter slot per type (see
// config.AddService's "multiple instances" guard), so this rejects a
// second service of a type that already exists, exactly like a static
// config.json would.
func (service *Service) AddService(ctx context.Context, candidate config.Service) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.AddService(currentCfg, candidate)
	if err != nil {
		return err
	}
	// The newly added entry is always the last one AddService appends.
	added := updatedCfg.Services[len(updatedCfg.Services)-1]

	if err := validateServiceConnection(ctx, added); err != nil {
		return fmt.Errorf("connection check failed: %w", err)
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.activateClientLocked(added)
	// The connection check just above already proved this service is
	// reachable; record that now instead of leaving the Services page to
	// show "Not checked yet" until the next scheduled refresh happens to run.
	service.setStatusLocked(added.Name, true, true, nil)
	service.mu.Unlock()
	return nil
}

// TestServiceConnection runs the same live connection check AddService and
// EditService already perform internally, but on its own — no config
// mutation, no persistence, no client activation. This is step 1 of the
// service setup overlay: fast feedback on whether the entered
// URL/credentials actually work, before showing step 2's extra fields.
func (service *Service) TestServiceConnection(ctx context.Context, candidate config.Service) error {
	return validateServiceConnection(ctx, candidate)
}

// EditService updates an existing service's Name/URL/credentials —
// re-validating the connection against the *new* values before persisting,
// exactly like AddService — and re-activates its client with them. The
// service's ID (and therefore everything already attributed to it) never
// changes, even when the URL does: moving a service to an entirely
// different address is exactly the point, not a side effect to guard against.
func (service *Service) EditService(ctx context.Context, id string, updates config.Service) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.EditService(currentCfg, id, updates)
	if err != nil {
		return err
	}
	var edited config.Service
	for _, svc := range updatedCfg.Services {
		if svc.ID == id {
			edited = svc
			break
		}
	}

	if err := validateServiceConnection(ctx, edited); err != nil {
		return fmt.Errorf("connection check failed: %w", err)
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	oldName := ""
	for _, svc := range currentCfg.Services {
		if svc.ID == id {
			oldName = svc.Name
			break
		}
	}
	service.cfg = updatedCfg
	service.activateClientLocked(edited)
	if oldName != "" && oldName != edited.Name {
		delete(service.statuses, oldName)
	}
	service.setStatusLocked(edited.Name, true, true, nil)
	service.mu.Unlock()
	return nil
}

// RemoveService deletes an existing service and deactivates its
// client immediately, without disturbing the Service's existing in-memory
// reconciliation state. Whatever it previously owned is not touched here —
// it simply stops matching any configured service on the next
// reconciliation and is reported as Unmanaged from then on.
func (service *Service) RemoveService(id string) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	var removed config.Service
	for _, svc := range currentCfg.Services {
		if svc.ID == id {
			removed = svc
			break
		}
	}

	updatedCfg, err := config.RemoveService(currentCfg, id)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.deactivateClientLocked(removed)
	delete(service.statuses, removed.Name)
	service.mu.Unlock()
	return nil
}

// SetDeviceThreshold idempotently persists one storage device's reclamation
// thresholds, name, and automatic-removal enablement together. Unlike
// AddService/EditService this isn't a live-service concept — no connection
// check runs, and no client is (de)activated. Editing the same device from
// any service overlay that shares it converges to the same
// config.SetDeviceThreshold entry.
func (service *Service) SetDeviceThreshold(representativePath, name string, target, critical float64, automaticRemovalEnabled bool) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.SetDeviceThreshold(currentCfg, representativePath, name, target, critical, automaticRemovalEnabled)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
	return nil
}

// syncDeviceRegistry assigns a stable id/default name to any newly
// discovered device and prunes entries for devices no longer discovered —
// see config.SyncDeviceRegistry. Called once at the end of a full file
// reconciliation (reconcileFiles), the one point with the complete live
// device set; must never be called from the targeted/delta reconciliation
// path, which only knows a partial scope. Best-effort: the authoritative
// file/media state has already been committed by the time this runs, so a
// persist failure here is logged rather than surfaced as a reconciliation
// failure — cosmetic device identity isn't worth discarding real work
// over.
func (service *Service) syncDeviceRegistry() {
	if service.configPath == "" {
		return
	}
	groups, order := service.knownDeviceRoots()
	livePaths := make([]string, 0, len(order))
	for _, device := range order {
		livePaths = append(livePaths, groups[device].representative)
	}

	service.configMu.Lock()
	defer service.configMu.Unlock()

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, changed := config.SyncDeviceRegistry(currentCfg, livePaths)
	if !changed {
		return
	}
	if err := config.Save(service.configPath, updatedCfg); err != nil {
		log.Printf("[inventory] persist device registry sync: %v", err)
		return
	}
	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
}

// Keep adds a title or torrent to the operator's Kept list, so it's never
// planned for removal until taken off it. key is config.KeptMediaKey's or
// config.KeptTorrentKey's; name is what the Storage page lists it as.
func (service *Service) Keep(key, name string) error {
	return service.changeKept(func(c config.Config) config.Config {
		return config.Keep(c, config.KeptItem{Key: key, Name: name, KeptAt: time.Now().UTC()})
	})
}

// Unkeep takes key off the Kept list.
func (service *Service) Unkeep(key string) error {
	return service.changeKept(func(c config.Config) config.Config { return config.Unkeep(c, key) })
}

func (service *Service) changeKept(change func(config.Config) config.Config) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()
	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}
	service.mu.RLock()
	updatedCfg := change(service.cfg)
	service.mu.RUnlock()
	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}
	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
	service.revalue()
	return nil
}

// importKeepTags brings what still carries an old keep tag (config
// Protection.KeepTags and KeepTorrentTags) into the Kept list, once, after
// an inventory, then empties the tag lists: from then on, keeping is done
// in Overtide, where it can be seen and undone.
func (service *Service) importKeepTags() {
	service.mu.RLock()
	cfg := service.cfg
	pending := len(cfg.Protection.KeepTags) > 0 || len(cfg.Protection.KeepTorrentTags) > 0
	service.mu.RUnlock()
	if !pending || service.configPath == "" {
		return
	}
	err := service.changeKept(func(c config.Config) config.Config {
		now := time.Now().UTC()
		for _, m := range service.items {
			if tag := firstTag(m.Tags, c.Protection.KeepTags); tag != "" {
				c = config.Keep(c, config.KeptItem{Key: config.KeptMediaKey(string(m.Type), m.ServiceID, m.SourceID), Name: mediaName(m), From: "keep tag " + tag, KeptAt: now})
			}
		}
		for _, t := range service.torrents {
			if tag := firstTag(strings.Split(t.Tags, ","), c.Protection.KeepTorrentTags); tag != "" {
				c = config.Keep(c, config.KeptItem{Key: config.KeptTorrentKey(t.Hash), Name: t.Name, From: "keep tag " + tag, KeptAt: now})
			}
		}
		c.Protection.KeepTags, c.Protection.KeepTorrentTags = nil, nil
		return c
	})
	if err != nil {
		log.Printf("[inventory] bring keep tags into the Kept list: %v", err)
	}
}

func firstTag(tags, wanted []string) string {
	for _, tag := range tags {
		for _, w := range wanted {
			if strings.EqualFold(strings.TrimSpace(tag), w) {
				return w
			}
		}
	}
	return ""
}

func mediaName(m model.Media) string {
	if m.Year > 0 {
		return fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	return m.Title
}

// revalue applies valuation again to what's in memory, after the operator
// changed what's kept or a protection setting: it takes effect at once,
// not at the next inventory refresh.
func (service *Service) revalue() {
	service.mu.Lock()
	items, torrents := cloneMedia(service.items), cloneTorrents(service.torrents)
	valuation.ApplyTorrentProtections(torrents, service.cfg, time.Now())
	projectTorrentRelations(items, torrents)
	valuation.ApplyMedia(items, service.cfg)
	service.items, service.torrents = items, torrents
	service.mu.Unlock()
	service.publishChange()
}

// SetRemovalSettings idempotently persists the global auto-removal switches
// (auto-removal is also still gated per-service by Service.
// AllowAutomaticRemoval — see EditService). Nothing here needs a connection
// check or an in-memory side effect beyond swapping the config: these
// flags only change what the next scheduled evaluation or removal
// submission does, never anything already published.
func (service *Service) SetRemovalSettings(removal config.RemovalConfig, privateMinSeedingDays int) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.SetRemovalSettings(currentCfg, removal, privateMinSeedingDays)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
	service.revalue() // the private seeding days change what's protected
	return nil
}

// buildRadarrClients/buildSonarrClients/buildQBittorrentClients construct one
// live client per configured instance of their type, keyed by the
// service's stable ID. Radarr/Sonarr/qBittorrent are the three types
// that support more than one configured instance (see
// config.multiInstanceAllowed); Overfin stays single-client.
func buildRadarrClients(configuration config.Config) map[string]*radarr.Client {
	out := map[string]*radarr.Client{}
	for _, svc := range configuration.ServicesOfType("radarr") {
		out[svc.ID] = radarr.New(svc.URL, svc.APIKey)
	}
	return out
}

func buildSonarrClients(configuration config.Config) map[string]*sonarr.Client {
	out := map[string]*sonarr.Client{}
	for _, svc := range configuration.ServicesOfType("sonarr") {
		out[svc.ID] = sonarr.New(svc.URL, svc.APIKey)
	}
	return out
}

func buildQBittorrentClients(configuration config.Config) map[string]*qbittorrent.Client {
	out := map[string]*qbittorrent.Client{}
	for _, svc := range configuration.ServicesOfType("qbittorrent") {
		out[svc.ID] = qbittorrent.New(svc.Name, svc.URL, svc.Username, svc.Password, svc.APIKey)
	}
	return out
}

// activateClientLocked (re)builds the client for service from its
// current fields and activates it under its stable ID (for the
// multi-instance types) or as the single client (Overfin). Called
// with service.mu already held for writing.
//
// The client maps are never changed in place: readers take a map under
// the lock and use it after letting go, so each change makes a new map.
func (service *Service) activateClientLocked(svc config.Service) {
	switch svc.Type {
	case "radarr":
		service.rad = maps.Clone(service.rad)
		service.rad[svc.ID] = radarr.New(svc.URL, svc.APIKey)
	case "sonarr":
		service.son = maps.Clone(service.son)
		service.son[svc.ID] = sonarr.New(svc.URL, svc.APIKey)
	case "overfin":
		// Its scores are the Retention Value: planning waits until they're
		// read with the new address or key.
		service.overfin = overfin.New(svc.URL, svc.APIKey)
		service.reliability.Overfin = "stale"
		service.reliability.Valuation = false
	case "qbittorrent":
		service.qb = maps.Clone(service.qb)
		service.qb[svc.ID] = qbittorrent.New(svc.Name, svc.URL, svc.Username, svc.Password, svc.APIKey)
	}
}

// deactivateClientLocked removes service's client entirely (for the
// multi-instance types, so a removed instance stops matching any future
// lookup by ID) or resets it to the "unconfigured" shape every
// reconciliation loop assumes is never nil (Overfin). Called with
// service.mu already held for writing. Like activateClientLocked, it makes
// new maps.
func (service *Service) deactivateClientLocked(svc config.Service) {
	switch svc.Type {
	case "radarr":
		service.rad = maps.Clone(service.rad)
		delete(service.rad, svc.ID)
	case "sonarr":
		service.son = maps.Clone(service.son)
		delete(service.son, svc.ID)
	case "qbittorrent":
		service.qb = maps.Clone(service.qb)
		delete(service.qb, svc.ID)
	case "overfin":
		// Nothing is valued without Overfin, so everything is protected at
		// once, rather than planned with Overfin's last scores.
		service.overfin = overfin.New("", "")
		if service.generation > 0 {
			items := cloneMedia(service.items)
			for i := range items {
				items[i].OverfinScore, items[i].OverfinSeasonScores, items[i].OverfinEnrichedAt = nil, nil, time.Time{}
			}
			valuation.ApplyMedia(items, service.cfg) // without Overfin already
			service.items = items
		}
		service.reliability.Overfin = "not configured"
		service.reliability.Valuation = service.reliability.valuationReady()
	}
}

func validateServiceConnection(ctx context.Context, svc config.Service) error {
	switch svc.Type {
	case "radarr":
		return radarr.New(svc.URL, svc.APIKey).WithContext(ctx).Validate()
	case "sonarr":
		return sonarr.New(svc.URL, svc.APIKey).WithContext(ctx).Validate()
	case "overfin":
		return overfin.New(svc.URL, svc.APIKey).WithContext(ctx).Validate()
	case "qbittorrent":
		return qbittorrent.New(svc.Name, svc.URL, svc.Username, svc.Password, svc.APIKey).WithContext(ctx).Validate()
	default:
		return fmt.Errorf("unsupported service type %q", svc.Type)
	}
}

// SetCredentials idempotently persists the single admin account's
// username/password (bcrypt-hashed by config.SetCredentials). Used for both
// first-run setup and later password changes — like SetDeviceThreshold this
// isn't a live-service concept, so no connection check or client
// (de)activation happens here.
func (service *Service) SetCredentials(username, password string) error {
	service.configMu.Lock()
	defer service.configMu.Unlock()

	if service.configPath == "" {
		return fmt.Errorf("live config editing is unavailable: no config file path is set")
	}

	service.mu.RLock()
	currentCfg := service.cfg
	service.mu.RUnlock()

	updatedCfg, err := config.SetCredentials(currentCfg, username, password)
	if err != nil {
		return err
	}

	if err := config.Save(service.configPath, updatedCfg); err != nil {
		return fmt.Errorf("persist config: %w", err)
	}

	service.mu.Lock()
	service.cfg = updatedCfg
	service.mu.Unlock()
	return nil
}
