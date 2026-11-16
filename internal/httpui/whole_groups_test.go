package httpui

import (
	"testing"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/model"
)

// Titles sharing a pack go together or not at all: one not cleared (held
// for a last watch, not on screen) keeps the others back.
func TestWholeGroups(t *testing.T) {
	film := func(id int) cleanup.Action {
		return cleanup.Action{Kind: cleanup.HardlinkedBundle, Media: model.Media{Type: model.Movie, ServiceID: "r", SourceID: id}}
	}
	a, b, c := film(1), film(2), film(3)
	a.Together, b.Together = []string{cleanup.ActionKey(b)}, []string{cleanup.ActionKey(a)}
	all := []cleanup.Action{a, b, c}
	if got := wholeGroups(all, func(cleanup.Action) bool { return true }); len(got) != 3 {
		t.Fatalf("all cleared: %d", len(got))
	}
	got := wholeGroups(all, func(x cleanup.Action) bool { return x.Media.SourceID != 2 })
	if len(got) != 1 || got[0].Media.SourceID != 3 {
		t.Fatalf("one of the pair held: %#v", got)
	}
}
