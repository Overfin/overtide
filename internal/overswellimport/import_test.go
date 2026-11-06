package overswellimport

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/store"
)

// overswell makes an Overswell config folder: its services, adding
// settings and saved values.
func overswell(t *testing.T, configJSON string, meta map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(dir, "state"), 0o700)
	db, err := sql.Open("sqlite", filepath.Join(dir, "state", "overswell.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for key, value := range meta {
		if _, err := db.Exec(`INSERT INTO metadata(key, value) VALUES(?, ?)`, key, value); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Services are matched by address when their IDs differ, and the IDs in
// the state follow; Prowlarr is added, Overfin takes Overswell's key, and
// a second import is refused.
func TestImport(t *testing.T) {
	from := overswell(t, `{
		"services": [
			{"type":"sonarr","name":"Series","url":"http://sonarr:8989/","api_key":"s","id":"sonarr-old"},
			{"type":"radarr","name":"Movies","url":"http://radarr:7878","api_key":"r","id":"radarr-same"},
			{"type":"prowlarr","name":"Trackers","url":"http://prowlarr:9696","api_key":"p","id":"prowlarr-old"},
			{"type":"overfin","name":"Overfin","url":"https://overfin.example","api_key":"wide","id":"overfin-old"},
			{"type":"overtide","name":"Janitor","url":"http://overtide:8088","api_key":"o","id":"overtide-old"}
		],
		"auth": {"username":"admin"},
		"adding": {"mode":"on","manage":"all","searches":25,"destinations":[{"service_id":"sonarr-old","root_folder":"/data/Series","quality_profile_id":7}]}
	}`, map[string]string{
		"adding.waiting":  `[{"serviceId":"sonarr-old","externalId":1},{"serviceId":"radarr-same","externalId":2}]`,
		"adding.episodes": `{"sonarr-old":{"follow":{"1":true}}}`,
		"import_choices":  `null`,
		"sessions.other":  `not adding's`,
	})
	cfg := config.Config{Services: []config.Service{
		{Type: "sonarr", Name: "Sonarr", URL: "http://sonarr:8989", APIKey: "s", ID: "sonarr-new"},
		{Type: "radarr", Name: "Radarr", URL: "http://radarr:7878", APIKey: "r", ID: "radarr-same"},
		{Type: "overfin", Name: "Overfin", URL: "https://overfin.example/", APIKey: "values-only", ID: "overfin-new"},
	}}
	db, err := store.Open(filepath.Join(t.TempDir(), "inventory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg, report, err := Import(cfg, db, from)
	if err != nil {
		t.Fatal(err)
	}
	if prowlarr, ok := cfg.FirstService("prowlarr"); !ok || prowlarr.URL != "http://prowlarr:9696" || prowlarr.APIKey != "p" {
		t.Errorf("prowlarr: %+v", prowlarr)
	}
	if overfin, _ := cfg.FirstService("overfin"); overfin.APIKey != "wide" || overfin.ID != "overfin-new" {
		t.Errorf("overfin: %+v", overfin)
	}
	if len(cfg.ServicesOfType("overtide")) != 0 {
		t.Error("Overswell's Overtide came along")
	}
	if cfg.Adding.Mode != config.AddingOn || cfg.Adding.Searches != 25 || len(cfg.Adding.Destinations) != 1 || cfg.Adding.Destinations[0].ServiceID != "sonarr-new" {
		t.Errorf("adding: %+v", cfg.Adding)
	}
	if waiting, _ := db.Meta("adding.waiting"); waiting != `[{"serviceId":"sonarr-new","externalId":1},{"serviceId":"radarr-same","externalId":2}]` {
		t.Errorf("waiting: %s", waiting)
	}
	if episodes, _ := db.Meta("adding.episodes"); !strings.Contains(episodes, `"sonarr-new"`) || strings.Contains(episodes, "sonarr-old") {
		t.Errorf("episodes: %s", episodes)
	}
	if other, _ := db.Meta("sessions.other"); other != "" {
		t.Errorf("not adding's came along: %s", other)
	}
	if len(report) == 0 {
		t.Error("nothing reported")
	}
	if _, _, err := Import(cfg, db, from); err == nil {
		t.Error("imported twice")
	}
}

// A Radarr or Sonarr Overtide doesn't have stops the import, before
// anything is written.
func TestImportNeedsTheSameServices(t *testing.T) {
	from := overswell(t, `{"services":[{"type":"radarr","name":"Movies","url":"http://elsewhere:7878","id":"radarr-x"}],"adding":{}}`, map[string]string{"adding.waiting": "[]"})
	db, err := store.Open(filepath.Join(t.TempDir(), "inventory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, _, err := Import(config.Config{}, db, from); err == nil || !strings.Contains(err.Error(), "add it in Services first") {
		t.Fatalf("%v", err)
	}
	if waiting, _ := db.Meta("adding.waiting"); waiting != "" {
		t.Error("state written anyway")
	}
}
