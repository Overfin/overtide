package removal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file already gone is listed without bytes or a warning; nothing is left
// to free.
func TestBuildListsAFileAlreadyGone(t *testing.T) {
	tempDir := t.TempDir()
	present := filepath.Join(tempDir, "present")
	if err := os.WriteFile(present, make([]byte, 500), 0644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(tempDir, "gone")
	plan := Build(MediaObject, "x", "x", true, []CandidateFile{
		{Path: gone, Selected: true, Owner: MediaOwner},
		{Path: present, Selected: true, Owner: MediaOwner},
	})
	if plan.ReclaimableBytes != 500 || plan.SelectedPathBytes != 500 {
		t.Fatalf("reclaimable %d, selected %d, want 500", plan.ReclaimableBytes, plan.SelectedPathBytes)
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("warnings %v", plan.Warnings)
	}
	if len(plan.Files) != 2 || plan.Files[0].Path != gone || plan.Files[0].Exists || plan.Files[0].Error != "" {
		t.Fatalf("files %+v", plan.Files)
	}
}

// A file that can't be read is listed with its error and a warning, and its
// size isn't guessed.
func TestBuildWarnsAboutAFileItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	tempDir := t.TempDir()
	locked := filepath.Join(tempDir, "locked")
	if err := os.Mkdir(locked, 0755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(locked, "a")
	if err := os.WriteFile(inside, make([]byte, 500), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0755) })
	plan := Build(MediaObject, "x", "x", true, []CandidateFile{{Path: inside, Selected: true, Owner: MediaOwner}})
	if plan.ReclaimableBytes != 0 || plan.SelectedPathBytes != 0 {
		t.Fatalf("reclaimable %d, selected %d, want 0", plan.ReclaimableBytes, plan.SelectedPathBytes)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], inside) {
		t.Fatalf("warnings %v", plan.Warnings)
	}
	if len(plan.Files) != 1 || plan.Files[0].Exists || plan.Files[0].Error == "" {
		t.Fatalf("files %+v", plan.Files)
	}
}

// The same file given twice, spelled two ways, is one link: selecting both
// spellings of one of two hardlinks doesn't free it.
func TestBuildCountsOnePathSpelledTwiceOnce(t *testing.T) {
	tempDir := t.TempDir()
	primaryPath := filepath.Join(tempDir, "a")
	if err := os.WriteFile(primaryPath, make([]byte, 1234), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(primaryPath, filepath.Join(tempDir, "b")); err != nil {
		t.Fatal(err)
	}
	plan := Build(MediaObject, "x", "x", true, []CandidateFile{
		{Path: primaryPath, Selected: true, Owner: MediaOwner},
		{Path: filepath.Join(tempDir, ".", "x", "..", "a"), Selected: true, Owner: MediaOwner},
	})
	if plan.ReclaimableBytes != 0 {
		t.Fatalf("one link spelled twice reclaimed %d", plan.ReclaimableBytes)
	}
}
