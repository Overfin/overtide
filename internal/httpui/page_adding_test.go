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

// The page renders one line per title, folds the groups that wait, and
// gives the library's titles as a count.
func TestAddingTemplateRenders(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	tpl := server.addingTpl
	waiting := adding.Entry{Key: "movie:1", Title: "Film", Year: 2026, Kind: "movie", From: "List", State: adding.Waiting, NextTry: time.Now(), Result: "Dry run: would add it"}
	data := addingPageData{Mode: "dry_run", Groups: []addingGroup{
		{Label: "Waiting", Open: true, Entries: []adding.Entry{waiting}},
		{Label: "In Radarr or Sonarr, without files", Adopt: true, Entries: []adding.Entry{{Key: "movie:2", Title: "Other"}}},
		{Label: "Already in the library", Count: true, Entries: []adding.Entry{{Key: "movie:3", Title: "Hidden"}}},
	}}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	for _, want := range []string{"DRY RUN", "<details class=\"card adding-group\" open>", "Hand them all to Overtide", "Hand over", "Already in the library"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(page, "Hidden") {
		t.Error("the library's titles were listed one by one")
	}
}
