package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoad reads any config.json, hand-edited or damaged: Load either
// refuses it or gives a Config every reader of it can use without a panic.
func FuzzLoad(f *testing.F) {
	f.Add([]byte(`{"services":[{"type":"radarr","name":"Movies","url":"http://radarr:7878","api_key":"k"}],"adding":{"mode":"on","titles_per_day":10},"storage":{"device_thresholds":{"/data":{"target_usage_percent":80,"critical_usage_percent":90,"folders":{"/data/movies":{"share_percent":60,"titles_per_day":3},"/data/tv":{"share_percent":40}},"roots":["/data","/data/movies"]}}}}`))
	f.Add([]byte(`{"storage":{"device_thresholds":{"":{"target_usage_percent":-1,"folders":{"":{"share_percent":1e308,"titles_per_day":-5,"upgrades_per_day":99999}}}}}}`))
	f.Add([]byte(`{"server":{"refresh_interval":"1ns","trusted_proxies":["192.0.2.0/24","::1"]}}`))
	f.Add([]byte(`{"services":[{"type":"jellyfin"},{"type":"qbittorrent","name":"a","url":"http://q"},{"type":"qbittorrent","name":"A","url":"http://q"}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			return
		}
		var paths []string
		roots := map[string][]string{}
		for path, threshold := range cfg.Storage.DeviceThresholds {
			paths = append(paths, path)
			var folders []string
			for folder := range threshold.Folders {
				folders = append(folders, folder)
				cfg.TitlesPerDayFor(folder)
				cfg.UpgradesPerDayFor(folder)
				cfg.FolderLimits(folder, 3, 1)
			}
			roots[path] = append(threshold.Roots, folders...)
			threshold.SharesOn()
			threshold.SharesAmong(append(folders, "/elsewhere"))
			cfg.ThresholdsFor(path)
			cfg.DeviceName(path)
			cfg.AutomaticRemovalEnabledFor(path)
		}
		if len(paths) > 0 {
			SyncDeviceRegistry(cfg, paths[1:], roots)
		}
		cfg.Adding.PerDay()
		cfg.Adding.SearchesPerRun()
		cfg.Adding.ManagesAll()
	})
}
