// Package overswellimport brings an Overswell's settings and state into
// Overtide, once, when the two become one app: its services matched with
// Overtide's, its adding settings, and what it kept (the waiting list,
// the episodes, the counts). Both apps must be stopped while it runs.
package overswellimport

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/store"

	_ "modernc.org/sqlite" // the SQLite driver, in pure Go
)

// overswellConfig is what's read of Overswell's config.json.
type overswellConfig struct {
	Services []config.Service   `json:"services"`
	Adding   config.AddingConfig `json:"adding"`
}

// Import reads the Overswell config folder from (config.json and
// state/overswell.db) into cfg and db. Radarr and Sonarr must be in
// Overtide already, matched by ID or address; a Prowlarr is added; the
// Overfin, the same server, takes Overswell's key, which may also read
// wishes and history. It refuses when adding's state is in db already.
// It returns the changed config, to save, and what was done.
func Import(cfg config.Config, db *store.Store, from string) (config.Config, []string, error) {
	waiting, err := db.Meta("adding.waiting")
	if err != nil {
		return cfg, nil, err
	}
	if waiting != "" {
		return cfg, nil, fmt.Errorf("adding's state is in Overtide already: nothing was imported")
	}
	raw, err := os.ReadFile(filepath.Join(from, "config.json"))
	if err != nil {
		return cfg, nil, err
	}
	var source overswellConfig
	if err := json.Unmarshal(raw, &source); err != nil {
		return cfg, nil, fmt.Errorf("read config.json: %w", err)
	}
	var report []string
	ids := map[string]string{} // Overswell's service ID → Overtide's
	for _, svc := range source.Services {
		switch svc.Type {
		case "radarr", "sonarr":
			match, ok := find(cfg, svc)
			if !ok {
				return cfg, nil, fmt.Errorf("the %s %q (%s) isn't in Overtide: add it in Services first", svc.Type, svc.Name, svc.URL)
			}
			ids[svc.ID] = match.ID
		case "prowlarr", "overfin":
			match, ok := cfg.FirstService(svc.Type)
			switch {
			case !ok:
				updated, err := config.AddService(cfg, config.Service{Type: svc.Type, Name: svc.Name, URL: svc.URL, APIKey: svc.APIKey})
				if err != nil {
					return cfg, nil, fmt.Errorf("add %s %q: %w", svc.Type, svc.Name, err)
				}
				cfg = updated
				added, _ := cfg.FirstService(svc.Type)
				ids[svc.ID] = added.ID
				report = append(report, fmt.Sprintf("Added %s %q", svc.Type, svc.Name))
			case !sameServer(match.URL, svc.URL):
				return cfg, nil, fmt.Errorf("the %s is at %s for Overswell, at %s for Overtide: make them the same first", svc.Type, svc.URL, match.URL)
			default:
				ids[svc.ID] = match.ID
				if svc.Type == "overfin" && svc.APIKey != "" && svc.APIKey != match.APIKey {
					edit := match
					edit.APIKey = svc.APIKey
					if cfg, err = config.EditService(cfg, match.ID, edit); err != nil {
						return cfg, nil, err
					}
					report = append(report, "Overfin's key is Overswell's now: it may also read wishes and history")
				}
			}
		}
	}

	adding := source.Adding
	adding.Destinations = nil
	for _, d := range source.Adding.Destinations {
		id, ok := ids[d.ServiceID]
		if !ok {
			return cfg, nil, fmt.Errorf("a destination's service (%s) isn't in Overswell's services", d.ServiceID)
		}
		d.ServiceID = id
		adding.Destinations = append(adding.Destinations, d)
	}
	cfg.Adding = adding
	report = append(report, fmt.Sprintf("Adding: mode %q, manage %q, %d destination(s)", adding.Mode, adding.Manage, len(adding.Destinations)))

	state, err := sql.Open("sqlite", "file:"+filepath.Join(from, "state", "overswell.db")+"?mode=ro")
	if err != nil {
		return cfg, nil, err
	}
	defer state.Close()
	rows, err := state.Query(`SELECT key, value FROM metadata WHERE key LIKE 'adding.%' OR key = 'import_choices'`)
	if err != nil {
		return cfg, nil, fmt.Errorf("read overswell.db: %w", err)
	}
	defer rows.Close()
	kept := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return cfg, nil, err
		}
		// Service IDs are random and unique, so they're replaced as text
		// wherever they are: map keys, fields, "id|movie|…" keys.
		for old, id := range ids {
			if old != id {
				value = strings.ReplaceAll(value, old, id)
			}
		}
		kept[key] = value
	}
	if err := rows.Err(); err != nil {
		return cfg, nil, err
	}
	for key, value := range kept {
		if err := db.SetMeta(key, value); err != nil {
			return cfg, nil, err
		}
	}
	report = append(report, fmt.Sprintf("State: %d saved value(s)", len(kept)))
	return cfg, report, nil
}

// find is Overtide's service matching one of Overswell's: the same ID,
// else the same type at the same address.
func find(cfg config.Config, svc config.Service) (config.Service, bool) {
	for _, existing := range cfg.ServicesOfType(svc.Type) {
		if existing.ID == svc.ID {
			return existing, true
		}
	}
	for _, existing := range cfg.ServicesOfType(svc.Type) {
		if sameServer(existing.URL, svc.URL) {
			return existing, true
		}
	}
	return config.Service{}, false
}

// sameServer reports whether two addresses reach the same server: the
// same scheme, host and port (a missing port is the scheme's own), and
// path.
func sameServer(a, b string) bool {
	ua, errA := url.Parse(strings.TrimRight(a, "/"))
	ub, errB := url.Parse(strings.TrimRight(b, "/"))
	if errA != nil || errB != nil {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Hostname(), ub.Hostname()) && port(ua) == port(ub) && ua.Path == ub.Path
}
