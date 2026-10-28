package httpui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
)

// TestFilesMissingRemovalAsksFirst: removing the torrents whose files are
// gone from qBittorrent goes through the app's confirmation dialog
// (data-confirm on the form, read by shell.ts), with a message that says
// how many and that no file is deleted — and, with dry run off, the
// dialog's button is a danger one.
func TestFilesMissingRemovalAsksFirst(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(inventory.New(cfg, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	torrents := []model.Torrent{{Name: "one"}, {Name: "two"}}

	render := func(dryRun bool) string {
		var out bytes.Buffer
		data := struct {
			Torrents []model.Torrent
			DryRun   bool
		}{torrents, dryRun}
		if err := server.filesMissingTpl.Execute(&out, data); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	live := render(false)
	for _, want := range []string{
		`data-confirm="Remove 2 torrents from qBittorrent? Their files are not deleted."`,
		`data-confirm-action="Remove"`,
		`data-confirm-danger`,
	} {
		if !strings.Contains(live, want) {
			t.Fatalf("expected %s in the dialog, got:\n%s", want, live)
		}
	}

	dry := render(true)
	if !strings.Contains(dry, `data-confirm="Simulate removing 2 torrents from qBittorrent?`) {
		t.Fatalf("expected the dry-run confirmation message, got:\n%s", dry)
	}
	if strings.Contains(dry, "data-confirm-danger") {
		t.Fatalf("a dry run removes nothing: its confirm button must not be a danger one, got:\n%s", dry)
	}
}
