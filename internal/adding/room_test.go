package adding

import (
	"testing"

	"github.com/overfin/overtide/internal/inventory"
)

// What the disks say of a folder stops adding there, or not: critical
// always does; unknown does except in dry run.
func TestDiskRoom(t *testing.T) {
	answers := map[string][2]string{
		"/ok":      {inventory.RoomOK, ""},
		"/full":    {inventory.RoomCritical, "Its disk is past its critical threshold"},
		"/unknown": {inventory.RoomUnknown, "Overtide doesn't know its disk"},
		"/silent":  {inventory.RoomCritical, ""},
	}
	room := DiskRoom(func(folder string) (string, string) { a := answers[folder]; return a[0], a[1] })
	for _, c := range []struct {
		folder string
		dryRun bool
		stops  bool
	}{
		{"/ok", false, false},
		{"/full", false, true},
		{"/full", true, true},
		{"/unknown", false, true},
		{"/unknown", true, false},
		{"/silent", false, true},
	} {
		if why := room(c.folder, c.dryRun); (why != "") != c.stops {
			t.Errorf("%s dry run %v: %q", c.folder, c.dryRun, why)
		}
	}
}
