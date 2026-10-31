package httpui

import (
	"testing"

	"github.com/overfin/overtide/internal/inventory"
)

// Home shows each disk at a glance: who it is, its bytes, or why it's
// unavailable. The per-service detail and the cleanup plan are the Storage
// page's, never Home's.
func TestHomeShowsStorageSummaryOnly(t *testing.T) {
	data := homeData{
		Devices: []deviceView{
			{
				Storage: inventory.StorageDevice{
					RootLabels: []string{"downloads", "movies"}, RepresentativePath: "/data/movies", Filesystem: "ext2/ext3/ext4",
					Available: true, TotalBytes: 1000, FreeBytes: 400, UsedBytes: 600,
					Claimed:        []inventory.ClaimedSegment{{Service: "Movies", Bytes: 500}},
					UnmanagedBytes: 50, OtherBytes: 50,
				},
			},
			{
				Storage: inventory.StorageDevice{RepresentativePath: "/data/broken", Available: false, Error: "permission denied"},
			},
		},
	}
	out := homeJSONFrom(data)
	if len(out.Devices) != 2 {
		t.Fatalf("devices: %+v", out.Devices)
	}
	disk := out.Devices[0]
	if disk.Filesystem != "ext2/ext3/ext4" || len(disk.RootLabels) != 2 || disk.RootLabels[0] != "downloads" || disk.RootLabels[1] != "movies" {
		t.Fatalf("identity: %+v", disk)
	}
	// "550.0 B used · 400.0 B free · 50.0 B used elsewhere, of 1000.0 B".
	if disk.OvertideUsedBytes != 550 || disk.FreeBytes != 400 || disk.ElsewhereBytes != 50 || disk.CapacityBytes != 1000 {
		t.Fatalf("bytes: %+v", disk)
	}
	if disk.Plan != nil {
		t.Fatalf("Home must not carry a cleanup plan: %+v", disk.Plan)
	}
	broken := out.Devices[1]
	if broken.Available || broken.Path != "/data/broken" || broken.Error != "permission denied" {
		t.Fatalf("unavailable device: %+v", broken)
	}
}

func TestHomeShowsServicesSummary(t *testing.T) {
	out := homeJSONFrom(homeData{Services: []inventory.ServiceStatus{{Name: "Movies", Configured: true, OK: true}, {Name: "Series", Configured: true}}})
	if len(out.Services) != 2 || out.Services[0] != (homeServiceJSON{Name: "Movies", OK: true}) || out.Services[1].OK {
		t.Fatalf("services: %+v", out.Services)
	}
}

// With no device known yet, Home says so: an empty list, never null.
func TestHomeWithNoKnownDevices(t *testing.T) {
	out := homeJSONFrom(homeData{})
	if out.Devices == nil || len(out.Devices) != 0 || out.Services == nil {
		t.Fatalf("%+v", out)
	}
}
