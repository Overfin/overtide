package inventory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/overfin/overtide/internal/config"
)

// The live config is replaced while other goroutines read it; under -race
// this fails if any of those readers skips the lock.
func TestConfigReadersDontRaceALiveChange(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"storage":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	service := New(baseCfg, nil)
	service.SetConfigPath(configPath)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if err := service.Keep(fmt.Sprintf("movie:1:%d", i), "Title"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = service.ValidateOverfin(context.Background())
			_ = service.RefreshOverfin(context.Background())
			_ = service.validateServiceTypes(context.Background(), map[string]bool{})
		}
	}()
	wg.Wait()
}
