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

// The page renders one line per title under headed columns, opens the
// groups that move, filters by source, and offers to hand over what Radarr
// and Sonarr have without files.
func TestAddingTemplateRenders(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	tpl := server.addingTpl
	waiting := addingRow{Title: "Film", Year: 2026, Kind: "Film", Source: "Overfin's picks", When: time.Now(), Result: "Dry run: would add it", DryRun: true}
	data := addingPageData{Mode: "dry_run", WithoutFiles: 64, Total: 2, Matching: 2, SelfAdding: []string{"Movies: Latest"}, Sources: []addingSourceCount{{Value: string(adding.FromOverfin), Label: "Overfin's picks", Count: 2}}, Groups: []addingGroup{
		{Label: "Waiting", WhenLabel: "Next try", Open: true, Rows: []addingRow{waiting}, Count: 60, MoreURL: "/adding?all=waiting"},
		{Label: "Imported", WhenLabel: "Since", Rows: []addingRow{{Title: "Other", Kind: "Film"}}, Count: 1},
	}}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	for _, want := range []string{"DRY RUN", "<details class=\"card adding-group\" open>", "<b>64</b> titles without files", "Hand them all to Overtide", "Film", "Other", "<th>Next try</th>", "dry-result", "Overfin&#39;s picks (2)", "Show all 60", "Turn off their automatic adding"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
}
