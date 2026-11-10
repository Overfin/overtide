// Package torrentdriver is a torrent client as Overtide uses it: its
// torrents, their files and where they're saved, removing one, and checking
// that files aren't the client's before Overtide deletes them itself.
// qBittorrent is the first client; its API's ways (sign-in, sync IDs, hash
// lists, the incomplete-downloads folder) stay inside its driver. Every call
// but Check is tried again when the connection fails for a moment (see
// retry).
package torrentdriver

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/overfin/overtide/internal/model"
)

// Driver is a torrent client.
type Driver interface {
	// Name is the service's name, as torrents carry it.
	Name() string
	WithContext(ctx context.Context) Driver
	// Torrents returns the torrents, from the previous list and the client's
	// cursor (0: everything again); the client gives back its new cursor.
	Torrents(previous map[string]model.Torrent, cursor int64) (map[string]model.Torrent, int64, error)
	// Detail is one torrent as the client has it now; ErrNotFound when it
	// has none with that hash.
	Detail(hash string) (model.Torrent, error)
	// Files are each torrent's files, by hash. A torrent the client no
	// longer has is left out.
	Files(torrents map[string]model.Torrent) (map[string][]File, error)
	// Roots are the folders the client saves into.
	Roots(torrents map[string]model.Torrent) ([]Root, error)
	// Remove deletes a torrent, with its data or without. A torrent already
	// gone is removed.
	Remove(hash string, withData bool) error
	// NotClaimed fails when one of the paths is a file of one of the
	// client's torrents now, asked of the client itself.
	NotClaimed(paths []string) error
	// ClaimedByOthers returns which of the paths a torrent not in except
	// (hashes, lower case) uses now, asked of the client itself.
	ClaimedByOthers(paths []string, except map[string]bool) ([]string, error)
	// Check tests the address and the sign-in, once.
	Check() error
}

// ErrNotFound: the client has no torrent with that hash.
var ErrNotFound = errors.New("torrent not found")

// File is a file of a torrent; Name is relative to FileRoot.
type File struct {
	Index    int
	Name     string
	Size     int64
	Progress float64
}

// Root is a folder the client saves into, and why it's one: Purpose is
// PurposeIncompleteDownloads for the folder in-progress downloads go to,
// empty otherwise.
type Root struct {
	Path    string
	Purpose string
}

// PurposeIncompleteDownloads labels the folder in-progress downloads go to:
// one easy to leave out of a Docker mount by accident.
const PurposeIncompleteDownloads = "incomplete downloads"

// Waits between the tries of a removal that failed for a moment: 4 tries
// in all, about a minute and a half, since a removal cut short leaves data
// behind. ReadWaits are the reads': 3 tries in 4 s, since the tasks that
// read try again themselves. Tests shorten both.
var (
	Waits     = []time.Duration{5 * time.Second, 20 * time.Second, time.Minute}
	ReadWaits = []time.Duration{time.Second, 3 * time.Second}
)

// retry runs call, and again after each of waits while it fails for a
// moment (Passing). It gives up at once on any other error, and when ctx
// ends.
func retry(ctx context.Context, waits []time.Duration, call func() error) error {
	err := call()
	for _, wait := range waits {
		if err == nil || !Passing(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
		err = call()
	}
	return err
}

// statusCoder is an error carrying the client's HTTP status.
type statusCoder interface{ StatusCode() int }

// Passing reports whether err is a failure of the moment, worth another
// try: the connection reset, refused or timed out, cut short, or the client
// answered it's busy or broken (5xx) or the session lapsed (403). A refusal
// (a wrong key, an unknown torrent, a bad request) isn't.
func Passing(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var status statusCoder
	if errors.As(err, &status) {
		code := status.StatusCode()
		return code >= 500 || code == 403 || code == 429
	}
	var netErr net.Error
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || (errors.As(err, &netErr) && netErr.Timeout())
}
