package adding

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/arr"
)

// With "Manage all", Radarr and Sonarr tell Overtide each title they add
// (a webhook in their Connect settings, which Overtide makes): the next
// run starts at once, and takes it (takeAll) without waiting its turn.
// The webhook signs in with a secret only Overtide and they know; the
// secret is kept in Overtide's database, so a copy of its settings file
// alone doesn't let anyone send events.

const (
	// EventsPath is where Radarr and Sonarr send their events, and
	// EventsUser the name they sign in with (the secret is the password).
	EventsPath = "/events/arr"
	EventsUser = "overtide"
	// webhookName is the Connect entry's name in Radarr and Sonarr.
	webhookName     = "Overtide"
	eventsSecretKey = "adding.events_secret"
)

// EventsSecret is the webhook's password, made the first time.
func (service *Service) EventsSecret() (string, error) {
	database := service.inv.Store()
	if database == nil {
		return "", fmt.Errorf("no database")
	}
	secret, err := database.Meta(eventsSecretKey)
	if err != nil || secret != "" {
		return secret, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret = hex.EncodeToString(b)
	return secret, database.SetMeta(eventsSecretKey, secret)
}

// SeenAt notes the address the panel was opened at (scheme and host), for
// findAddress to try first: what reaches Overtide from the operator's
// browser often reaches it from Radarr and Sonarr too.
func (service *Service) SeenAt(address string) {
	service.eventsMu.Lock()
	service.seenAt = address
	service.eventsMu.Unlock()
}

// HeardTest notes that a test event came, the webhook's secret right.
func (service *Service) HeardTest() {
	service.eventsMu.Lock()
	service.heard = time.Now()
	service.eventsMu.Unlock()
}

func (service *Service) heardSince(t time.Time) bool {
	service.eventsMu.Lock()
	defer service.eventsMu.Unlock()
	return service.heard.After(t)
}

// EventsProblem is what keeps Radarr or Sonarr from sending Overtide
// their events, "" when nothing does: shown with the setting.
func (service *Service) EventsProblem() string {
	service.eventsMu.Lock()
	defer service.eventsMu.Unlock()
	return service.eventsProblem
}

func (service *Service) setEventsProblem(problem string) {
	service.eventsMu.Lock()
	service.eventsProblem = problem
	service.eventsMu.Unlock()
}

// eventsApp is a Radarr or a Sonarr, and the event that says it added a
// title.
type eventsApp struct {
	svc    config.Service
	client *arr.Client
	event  string
}

// connect makes sure every Radarr and Sonarr sends Overtide the titles it
// adds, with "Manage all" and adding on (in dry run, nothing changes in
// them). Without an address set, Overtide tries those it may have and keeps
// the first one a test event comes through.
func (service *Service) connect(ctx context.Context, cfg config.Config, dryRun bool) error {
	if !cfg.Adding.ManagesAll() || dryRun {
		return nil
	}
	service.connectMu.Lock()
	defer service.connectMu.Unlock()
	secret, err := service.EventsSecret()
	if err != nil {
		return err
	}
	var apps []eventsApp
	for _, svc := range cfg.ServicesOfType("radarr") {
		client, err := service.inv.RadarrClient(svc.ID)
		if err != nil {
			return err
		}
		apps = append(apps, eventsApp{svc, client.WithContext(ctx).Client, "onMovieAdded"})
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		client, err := service.inv.SonarrClient(svc.ID)
		if err != nil {
			return err
		}
		apps = append(apps, eventsApp{svc, client.WithContext(ctx).Client, "onSeriesAdd"})
	}
	if len(apps) == 0 {
		return nil
	}
	address := cfg.Adding.EventsURL
	if address == "" {
		if address = service.findAddress(apps[0], cfg, secret); address == "" {
			service.setEventsProblem(fmt.Sprintf("%s couldn't reach Overtide at any address it could have: set the one it reaches Overtide at.", apps[0].svc.Name))
			return nil
		}
		if err := service.inv.SetEventsURL(address); err != nil {
			return err
		}
		log.Printf("[adding] Radarr and Sonarr reach Overtide at %s: kept for their events", address)
	}
	for _, app := range apps {
		if err := service.saveWebhook(app, address, secret); err != nil {
			service.setEventsProblem(fmt.Sprintf("%s: %v", app.svc.Name, err))
			return nil
		}
	}
	service.setEventsProblem("")
	return nil
}

// Connect is connect for the Settings page, which brings an address to try
// (SeenAt): run at once, apart from the hourly task, while no address is
// known.
func (service *Service) Connect(ctx context.Context) {
	cfg := service.inv.Config()
	if cfg.Adding.EventsURL != "" || (cfg.Adding.Mode != config.AddingOn) {
		return
	}
	if err := service.connect(ctx, cfg, false); err != nil {
		log.Printf("[adding] Radarr's and Sonarr's events: %v", err)
	}
}

// webhook is the Connect entry that sends app's added titles to address:
// entry (the existing one, or the app's schema) with Overtide's values.
func webhook(entry map[string]any, app eventsApp, address, secret string) map[string]any {
	entry["name"] = webhookName
	for key := range entry {
		if strings.HasPrefix(key, "on") {
			if _, isBool := entry[key].(bool); isBool {
				entry[key] = false
			}
		}
	}
	entry[app.event] = true
	entry["tags"] = []int{}
	arr.SetField(entry, "url", address+EventsPath)
	arr.SetField(entry, "method", 1) // POST
	arr.SetField(entry, "username", EventsUser)
	arr.SetField(entry, "password", secret)
	return entry
}

// saveWebhook makes or corrects app's Connect entry, once per address in a
// run of Overtide: what the app shows of the password can't be compared.
func (service *Service) saveWebhook(app eventsApp, address, secret string) error {
	key := app.svc.ID + "|" + address
	service.eventsMu.Lock()
	done := service.connected[key]
	service.eventsMu.Unlock()
	if done {
		return nil
	}
	entries, err := app.client.Notifications()
	if err != nil {
		return err
	}
	var entry map[string]any
	for _, e := range entries {
		if name, _ := e["name"].(string); name == webhookName {
			entry = e
		}
	}
	if entry == nil {
		if entry, err = app.client.WebhookSchema(); err != nil {
			return err
		}
	}
	if err := app.client.SaveNotification(webhook(entry, app, address, secret)); err != nil {
		return err
	}
	service.eventsMu.Lock()
	service.connected[key] = true
	service.eventsMu.Unlock()
	return nil
}

// findAddress tries the addresses Overtide may have as app reaches it: the
// one the panel was last opened at (SeenAt), then, on the port it listens
// on, its host name and "overtide" (on the same Docker network) and the
// app's own host (on the same machine). The first one a test event comes
// through wins; "" when none does.
func (service *Service) findAddress(app eventsApp, cfg config.Config, secret string) string {
	var addresses []string
	service.eventsMu.Lock()
	if service.seenAt != "" {
		addresses = append(addresses, service.seenAt)
	}
	service.eventsMu.Unlock()
	if _, port, err := net.SplitHostPort(cfg.Server.Listen); err == nil && port != "" {
		var hosts []string
		if name, err := os.Hostname(); err == nil && name != "" {
			hosts = append(hosts, name)
		}
		hosts = append(hosts, "overtide")
		if u, err := url.Parse(app.svc.URL); err == nil && u.Hostname() != "" {
			hosts = append(hosts, u.Hostname())
		}
		for _, host := range hosts {
			addresses = append(addresses, "http://"+net.JoinHostPort(host, port))
		}
	}
	schema, err := app.client.WebhookSchema()
	if err != nil {
		return ""
	}
	tried := map[string]bool{}
	for _, address := range addresses {
		if tried[address] {
			continue
		}
		tried[address] = true
		// The app sends the test event before it answers: heard by then,
		// or not at all (the address may be some other server's).
		start := time.Now()
		if err := app.client.TestNotification(webhook(schema, app, address, secret)); err == nil && service.heardSince(start) {
			return address
		}
	}
	return ""
}
