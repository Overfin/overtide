package config

import "testing"

func intp(n int) *int { return &n }

func TestDeviceFolders(t *testing.T) {
	c := Config{}
	c.Adding.UpgradesPerDay = 20 // set server-wide before folders had their own
	if c.TitlesPerDayFor("/data/Films") != DefaultTitlesPerDay || c.UpgradesPerDayFor("/data/Films") != 20 {
		t.Fatal("a folder without its own limits takes the server-wide ones")
	}
	if _, err := SetDeviceFolders(c, "/data/Films", map[string]FolderSettings{"/data/Films": {SharePercent: 60}, "/data/Series": {SharePercent: 30}}); err == nil {
		t.Fatal("shares adding up to 90% were taken")
	}
	updated, err := SetDeviceFolders(c, "/data/Films", map[string]FolderSettings{
		"/data/Films":  {SharePercent: 60, TitlesPerDay: intp(3), UpgradesPerDay: intp(0)},
		"/data/Series": {SharePercent: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Storage.DeviceThresholds["/data/Films"].SharesOn() {
		t.Fatal("shares not on")
	}
	if updated.TitlesPerDayFor("/data/Films") != 3 || updated.UpgradesPerDayFor("/data/Films") != 0 || updated.UpgradesPerDayFor("/data/Series") != 20 {
		t.Fatal("folder limits not read back")
	}
	// The thresholds saved after keep the folders.
	again, err := SetDeviceThreshold(updated, "/data/Films", "Media", 80, 90, true)
	if err != nil || len(again.Storage.DeviceThresholds["/data/Films"].Folders) != 2 {
		t.Fatalf("folders lost: %v", err)
	}
	off, err := SetDeviceFolders(again, "/data/Films", map[string]FolderSettings{"/data/Films": {TitlesPerDay: intp(5)}, "/data/Series": {}})
	if err != nil || off.Storage.DeviceThresholds["/data/Films"].SharesOn() {
		t.Fatalf("all shares 0 must be off: %v", err)
	}
	if _, err := SetDeviceFolders(c, "/data/Films", map[string]FolderSettings{"/data/Films": {TitlesPerDay: intp(0)}}); err == nil {
		t.Fatal("0 titles a day was taken")
	}
}
