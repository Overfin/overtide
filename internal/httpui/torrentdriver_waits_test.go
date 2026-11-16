package httpui

import (
	"os"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/torrentdriver"
	"golang.org/x/crypto/bcrypt"
)

// TestMain makes the torrent driver's tries again immediate: tests with a
// torrent client that fails would otherwise wait for its real schedule.
// Passwords are hashed at bcrypt's lowest cost, for the same reason.
func TestMain(m *testing.M) {
	config.PasswordCost = bcrypt.MinCost
	torrentdriver.Waits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	torrentdriver.ReadWaits = []time.Duration{time.Millisecond, time.Millisecond}
	os.Exit(m.Run())
}
