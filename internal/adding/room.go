package adding

import "github.com/overfin/overtide/internal/inventory"

// DiskRoom is a Room from what the inventory knows of the disks: a folder
// whose disk is past its critical threshold gets nothing new. A disk that
// can't be read or isn't known stops adding too, except in dry run, which
// adds nothing.
func DiskRoom(room func(folder string) (state, reason string)) func(rootFolder string, dryRun bool) string {
	return func(rootFolder string, dryRun bool) string {
		state, reason := room(rootFolder)
		switch {
		case state == inventory.RoomOK:
			return ""
		case state == inventory.RoomUnknown && dryRun:
			return ""
		case reason != "":
			return reason
		}
		return "Its disk has no room (" + state + ")"
	}
}
