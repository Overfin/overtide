package httpui

import (
	"testing"

	"github.com/overfin/overtide/internal/adding"
)

// Folders without upgrades a day of their own share one count: the page is
// told so, not shown the shared count as each one's own.
func TestUpgradesFoldersSayWhatsShared(t *testing.T) {
	got := upgradesFolders([]adding.FolderDay{
		{Root: "/media/movies", UpgradesPerDay: 2, Upgrades: 1, SharedUpgrades: true},
		{Root: "/media/tv", UpgradesPerDay: 2, Upgrades: 1, SharedUpgrades: true},
		{Root: "/media/anime", UpgradesPerDay: 5, Upgrades: 0},
	})
	if len(got) != 3 || !got[0].Shared || !got[1].Shared || got[2].Shared || got[2].PerDay != 5 {
		t.Fatalf("%+v", got)
	}
}
