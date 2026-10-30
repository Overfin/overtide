package inventory

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Room states: what Room says of a folder's disk.
const (
	RoomOK       = "ok"
	RoomCritical = "critical"
	RoomUnknown  = "unknown"
)

// Room says whether titles may be added to a folder (a Radarr or Sonarr
// root folder, as they see it) now, and why not. The usage target is what
// removal steers toward, not a stop: adding goes on past it and removal
// makes room after. Only the critical threshold stops adding, until
// removal catches up, so downloads can't fill the disk. A disk that can't
// be read now, or a folder on no disk Overtide knows, is RoomUnknown.
func (service *Service) Room(folder string) (state, reason string) {
	root := filepath.Clean(folder)
	cfg := service.Config()
	for _, device := range service.StorageDevices() {
		for _, paths := range device.ServiceRoots {
			for _, path := range paths {
				path = filepath.Clean(path)
				if root != path && !strings.HasPrefix(root, path+string(filepath.Separator)) && !strings.HasPrefix(path, root+string(filepath.Separator)) {
					continue
				}
				usable := device.UsableBytes()
				if !device.Available || usable == 0 {
					return RoomUnknown, "Its disk can't be read now"
				}
				_, critical := cfg.ThresholdsFor(device.RepresentativePath)
				if used := float64(device.OvertideUsedBytes()) / float64(usable) * 100; used >= critical {
					return RoomCritical, fmt.Sprintf("Its disk is past its critical threshold (%.1f%% used, critical %.1f%%)", used, critical)
				}
				return RoomOK, ""
			}
		}
	}
	return RoomUnknown, "Overtide doesn't know its disk (" + folder + ")"
}
