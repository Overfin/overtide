package inventory

import (
	"testing"

	"github.com/overfin/overtide/internal/config"
)

// A root folder added after the shares were saved gets its part of the
// disk, not 0%; one gone gives its part to the rest.
func TestShareOfFoldersAddedOrGone(t *testing.T) {
	var cfg config.Config
	cfg.Storage.DeviceThresholds = map[string]config.DeviceThreshold{"/data/films": {TargetUsagePercent: 50, CriticalUsagePercent: 95,
		Folders: map[string]config.FolderSettings{"/data/films": {SharePercent: 50}, "/data/gone": {SharePercent: 50}}}}
	device := StorageDevice{RepresentativePath: "/data/films", TotalBytes: 1000, Available: true,
		Folders: []FolderUsage{{Path: "/data/films", Bytes: 100}, {Path: "/data/series", Bytes: 100}}}
	for _, folder := range []string{"/data/films", "/data/series"} {
		bytes, limit, on := device.Share(cfg, folder)
		if !on || bytes != 100 || limit != 250 {
			t.Errorf("%s: %d of %d, shared %v", folder, bytes, limit, on)
		}
	}
}
