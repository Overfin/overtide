package inventory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

func keepTestService(t *testing.T, body string) (*Service, string) {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(cfg, nil)
	service.SetConfigPath(configPath)
	return service, configPath
}

// Keeping a title protects it at once, not at the next inventory, and
// survives a restart; unkeeping undoes both.
func TestKeepProtectsAtOnceAndPersists(t *testing.T) {
	service, configPath := keepTestService(t, `{"storage":{}}`)
	service.items = []model.Media{{Type: model.Movie, ServiceID: "r1", SourceID: 42, Title: "Film"}}
	key := config.KeptMediaKey("movie", "r1", 42)

	if err := service.Keep(key, "Film (2020)"); err != nil {
		t.Fatal(err)
	}
	items, _, _ := service.Snapshot()
	if !items[0].Protected || items[0].ProtectionReason != "Kept" {
		t.Fatalf("expected the film protected at once: %#v", items[0])
	}
	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsKept(key) || reloaded.Kept[0].Name != "Film (2020)" {
		t.Fatalf("expected the film on the saved list: %#v", reloaded.Kept)
	}

	if err := service.Unkeep(key); err != nil {
		t.Fatal(err)
	}
	items, _, _ = service.Snapshot()
	if items[0].ProtectionReason == "Kept" {
		t.Fatalf("expected the film no longer kept: %#v", items[0])
	}
}

// Old keep tags are brought into the Kept list once, then emptied.
func TestImportKeepTags(t *testing.T) {
	service, configPath := keepTestService(t, `{"storage":{},"protection":{"keep_tags":["keep"],"keep_torrent_tags":["stewarr_keep"]}}`)
	service.items = []model.Media{
		{Type: model.Movie, ServiceID: "r1", SourceID: 1, Title: "Tagged", Year: 2001, Tags: []string{"Keep"}},
		{Type: model.Movie, ServiceID: "r1", SourceID: 2, Title: "Untagged"},
	}
	service.torrents = []model.Torrent{{Hash: "AAA", Name: "Release", Tags: "x, stewarr_keep"}}

	service.importKeepTags()

	reloaded, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.IsKept(config.KeptMediaKey("movie", "r1", 1)) || reloaded.IsKept(config.KeptMediaKey("movie", "r1", 2)) || !reloaded.IsKept(config.KeptTorrentKey("aaa")) {
		t.Fatalf("kept: %#v", reloaded.Kept)
	}
	if reloaded.Kept[0].From != "keep tag keep" || reloaded.Kept[0].Name != "Tagged (2001)" {
		t.Fatalf("first item: %#v", reloaded.Kept[0])
	}
	if len(reloaded.Protection.KeepTags) != 0 || len(reloaded.Protection.KeepTorrentTags) != 0 {
		t.Fatalf("expected the tag lists emptied: %#v", reloaded.Protection)
	}
}
