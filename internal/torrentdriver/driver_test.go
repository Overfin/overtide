package torrentdriver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/services/qbittorrent"
)

func TestPassing(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("post: %w", syscall.ECONNRESET), true},
		{fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		{io.ErrUnexpectedEOF, true},
		{&qbittorrent.StatusError{Code: 503}, true},
		{&qbittorrent.StatusError{Code: 403}, true},
		{&qbittorrent.StatusError{Code: 400}, false},
		{qbittorrent.ErrTorrentNotFound, false},
		{context.Canceled, false},
		{errors.New("wrong password"), false},
	} {
		if got := Passing(c.err); got != c.want {
			t.Errorf("%v: %v", c.err, got)
		}
	}
}

// A delete cut short by the connection is sent again and succeeds; a
// refusal isn't sent again.
func TestRemoveTriesAgain(t *testing.T) {
	defer func(old []time.Duration) { Waits = old }(Waits)
	Waits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	var deletes atomic.Int32
	refuse := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/torrents/delete" {
			w.WriteHeader(http.StatusOK)
			return
		}
		n := deletes.Add(1)
		switch {
		case refuse:
			w.WriteHeader(http.StatusBadRequest)
		case n == 1:
			// The connection drops, as qBittorrent's did.
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	driver := QBittorrent("Downloads", srv.URL, "", "", "key")
	hash := "0123456789abcdef0123456789abcdef01234567"
	if err := driver.Remove(hash, true); err != nil || deletes.Load() != 2 {
		t.Fatalf("after a reset: %v, %d deletes", err, deletes.Load())
	}
	deletes.Store(0)
	refuse = true
	if err := driver.Remove(hash, true); err == nil || deletes.Load() != 1 {
		t.Fatalf("a refusal: %v, %d deletes", err, deletes.Load())
	}
}

// Check answers at once, a failing address included.
func TestCheckDoesNotWait(t *testing.T) {
	started := time.Now()
	if err := QBittorrent("x", "http://127.0.0.1:1", "", "", "key").Check(); err == nil {
		t.Fatal("an address nothing answers passed")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("Check waited")
	}
}
