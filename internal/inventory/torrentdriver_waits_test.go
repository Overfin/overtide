package inventory

import (
	"os"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/torrentdriver"
)

// TestMain makes the torrent driver's tries again immediate: tests with a
// torrent client that fails would otherwise wait for its real schedule.
func TestMain(m *testing.M) {
	torrentdriver.Waits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	torrentdriver.ReadWaits = []time.Duration{time.Millisecond, time.Millisecond}
	os.Exit(m.Run())
}
