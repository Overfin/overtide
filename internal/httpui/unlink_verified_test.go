package httpui

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/removal"
	"github.com/overfin/overtide/internal/store"
)

// The last check before a file leaves the disk: only the regular file
// confirmed (same device and inode) goes. A file put in its place after
// confirmation, a symbolic link (whose target stays), a folder, or a file
// whose identity wasn't known, stays.
func TestUnlinkVerifiedRemovesOnlyTheConfirmedFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	state := func(p string) removal.FileState {
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		return removal.FileState{Path: p, Exists: true, IdentityKnown: true, Device: uint64(st.Dev), Inode: st.Ino, SizeBytes: info.Size()}
	}
	confirmed := write("confirmed.mkv")
	replaced := write("replaced.mkv")
	replacedState := state(replaced)
	// The old file kept elsewhere, so its inode can't be the new one's.
	if err := os.Link(replaced, filepath.Join(t.TempDir(), "kept")); err != nil {
		t.Fatal(err)
	}
	os.Remove(replaced)
	write("replaced.mkv") // another file now, at the same path
	target := write("target.mkv")
	link := filepath.Join(dir, "link.mkv")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(dir, "folder.mkv")
	os.Mkdir(folder, 0o700)
	unknown := write("unknown.mkv")
	unknownState := state(unknown)
	unknownState.IdentityKnown = false

	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	server := &Server{inv: inventory.New(config.Config{}, database)}
	results, errs := server.unlinkVerified(context.Background(), []removal.FileState{
		state(confirmed), replacedState, state(link), {Path: folder, Exists: true, IdentityKnown: true}, unknownState,
	})
	if len(results) != 1 || len(errs) != 4 {
		t.Errorf("results %v, errors %v", results, errs)
	}
	if _, err := os.Lstat(confirmed); !os.IsNotExist(err) {
		t.Errorf("the confirmed file stayed: %v", err)
	}
	for _, p := range []string{replaced, link, target, folder, unknown} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s gone: %v", filepath.Base(p), err)
		}
	}
}
