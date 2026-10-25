package httpui

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
)

// The page renders one line per title, opens the groups that move, and
// offers to hand over what Radarr and Sonarr have without files.
func TestAddingTemplateRenders(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	tpl := server.addingTpl
	waiting := adding.Entry{Key: "movie:1", Title: "Film", Year: 2026, Kind: "movie", From: "List", State: adding.Waiting, NextTry: time.Now(), Result: "Dry run: would add it"}
	data := addingPageData{Mode: "dry_run", WithoutFiles: 64, Groups: []addingGroup{
		{Label: "Waiting", Open: true, Entries: []adding.Entry{waiting}},
		{Label: "Imported", Entries: []adding.Entry{{Key: "movie:2", Title: "Other"}}},
	}}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	for _, want := range []string{"DRY RUN", "<details class=\"card adding-group\" open>", "<b>64</b> titles without files", "Hand them all to Overtide", "Film", "Other"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
}
