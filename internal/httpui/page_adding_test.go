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
// groups that move, and filters by source.
func TestAddingTemplateRenders(t *testing.T) {
	server, err := New(inventory.New(config.Config{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	tpl := server.addingTpl
	waiting := addingRow{Title: "Film", Year: 2026, Kind: "Film", Source: "Overfin", When: time.Now(), Result: "Dry run: would add it", DryRun: true}
	data := addingPageData{Mode: "dry_run", Total: 2, Matching: 2, Sources: []addingSourceCount{{Value: string(adding.FromOverfin), Label: "Overfin", Count: 2}}, Groups: []addingGroup{
		{Label: "Waiting", WhenLabel: "Next try", Open: true, Rows: []addingRow{waiting}, Count: 60, MoreURL: "/adding?all=waiting"},
		{Label: "Imported", WhenLabel: "Since", Rows: []addingRow{{Title: "Other", Kind: "Film"}}, Count: 1},
	}}
	var out bytes.Buffer
	if err := tpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	page := out.String()
	for _, want := range []string{"DRY RUN", "<details class=\"card adding-group\" open>", "Film", "Other", "<th>Next try</th>", "dry-result", "Overfin (2)", "Show all 60"} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q", want)
		}
	}
}
