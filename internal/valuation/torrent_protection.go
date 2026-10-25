package valuation

import (
	"fmt"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// NewTorrentDays is how long a torrent counts as too new to remove on its
// own: time for a download someone started by hand, or a cross-seed being
// added, to settle.
const NewTorrentDays = 7

// ApplyTorrentProtections says, for each torrent, whether it may be
// removed: its service must allow removal, an obligation keeps its files
// (see torrentObligation), and one added less than NewTorrentDays ago is
// kept too, though without holding back the title it's hardlinked to.
func ApplyTorrentProtections(torrents []model.Torrent, configuration config.Config, now time.Time) {
	for i := range torrents {
		torrent := &torrents[i]
		torrent.RemovalRestricted = !serviceAllowsRemoval(configuration, torrent.ServiceID)
		torrent.Obligation = torrentObligation(*torrent, configuration)
		torrent.Protected = torrent.Obligation != ""
		torrent.ProtectionReason = torrent.Obligation
		if torrent.Protected {
			continue
		}
		if torrent.AddedOn <= 0 {
			torrent.Protected, torrent.ProtectionReason = true, "Date added unknown"
			continue
		}
		if age := now.Sub(time.Unix(torrent.AddedOn, 0)); age < NewTorrentDays*24*time.Hour {
			torrent.Protected = true
			torrent.ProtectionReason = fmt.Sprintf("Added %d days ago (kept %d days)", int(age.Hours()/24), NewTorrentDays)
		}
	}
}

// torrentObligation says why a torrent's files must stay, or "" if
// nothing requires it: kept by the operator, a private tracker's seeding
// time, the minimum ratio.
func torrentObligation(torrent model.Torrent, configuration config.Config) string {
	switch {
	case configuration.IsKept(config.KeptTorrentKey(torrent.Hash)):
		return "Kept"
	case torrent.Private && configuration.Protection.PrivateMinSeedingDays > 0 &&
		torrent.SeedingTime < int64(configuration.Protection.PrivateMinSeedingDays)*86400:
		return fmt.Sprintf("Private tracker: seeding %d of %d days", torrent.SeedingTime/86400, configuration.Protection.PrivateMinSeedingDays)
	case configuration.Protection.MinTorrentRatio > 0 && torrent.Ratio < configuration.Protection.MinTorrentRatio:
		return "Below minimum ratio"
	}
	return ""
}
