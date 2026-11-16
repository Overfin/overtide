package inventory

import (
	"fmt"

	"github.com/overfin/overtide/internal/config"
	"path/filepath"
	"strings"
)

// Room states: what Room says of a folder's disk.
const (
	RoomOK       = "ok"
	RoomCritical = "critical"
	RoomUnknown  = "unknown"
	// RoomOverShare: the folder holds more than its share of the disk.
	RoomOverShare = "over-share"
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
				if folder := device.FolderOf(root); folder != "" {
					if bytes, limit, on := device.Share(cfg, folder); on && bytes > limit {
						return RoomOverShare, fmt.Sprintf("Its root folder %s is over its share of the disk (%s, share %s)", folder, human(bytes), human(limit))
					}
				}
				return RoomOK, ""
			}
		}
	}
	return RoomUnknown, "Overtide doesn't know its disk (" + folder + ")"
}

// Share is what a root folder of the device holds and its share of the
// disk's space up to the target, in bytes; false when the disk's space
// isn't shared between its folders.
func (d StorageDevice) Share(cfg config.Config, folder string) (bytes, limit uint64, on bool) {
	threshold := cfg.Storage.DeviceThresholds[d.RepresentativePath]
	if !threshold.SharesOn() {
		return 0, 0, false
	}
	target, _ := cfg.ThresholdsFor(d.RepresentativePath)
	share := d.Shares(cfg)[folder]
	for _, f := range d.Folders {
		if f.Path == folder {
			bytes = f.Bytes
		}
	}
	return bytes, uint64(float64(d.UsableBytes()) * target / 100 * share / 100), true
}

// Shares is each of the device's root folders' share of its space, in
// percent, nil when it isn't shared (see config.DeviceThreshold.SharesAmong:
// folders added or gone since the shares were saved are counted in).
func (d StorageDevice) Shares(cfg config.Config) map[string]float64 {
	paths := make([]string, 0, len(d.Folders))
	for _, f := range d.Folders {
		paths = append(paths, f.Path)
	}
	return cfg.Storage.DeviceThresholds[d.RepresentativePath].SharesAmong(paths)
}

// human writes a size in binary units, one decimal.
func human(bytes uint64) string {
	value := float64(bytes)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for value >= 1024 && i < len(units)-1 {
		value /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", value, units[i])
}
