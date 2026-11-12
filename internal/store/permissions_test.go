package store

import (
	"os"
	"path/filepath"
	"testing"
)

// The database, and its folder, are Overtide's user's alone (SECURITY.md
// rule 30), an older Overtide's looser ones included.
func TestDatabasePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "inventory.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, f := range []string{dir, path} {
		info, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s: %v", f, info.Mode().Perm())
		}
	}
}
