package videolength

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestOf(t *testing.T) {
	for _, name := range []string{"clip.mkv", "clip.mp4", "clip-faststart.mp4"} {
		got, err := Of("testdata/" + name)
		if err != nil || got < 3300*time.Millisecond || got > 3500*time.Millisecond {
			t.Errorf("%s: %v, %v", name, got, err)
		}
	}
	if _, err := Of("testdata/missing.mkv"); err == nil {
		t.Error("a missing file has a length")
	}
}

// Not a video, cut short or made up: no length, and no panic.
func TestNoLength(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("hello, world"),
		{0x1A, 0x45, 0xDF, 0xA3},
		{0x1A, 0x45, 0xDF, 0xA3, 0x80, 0x18, 0x53, 0x80, 0x67, 0xFF},
		{0, 0, 0, 1, 'f', 't', 'y', 'p', 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		{0, 0, 0, 8, 'f', 't', 'y', 'p', 0, 0, 0, 8, 'm', 'o', 'o', 'v'},
	} {
		if d, err := Read(bytes.NewReader(data), int64(len(data))); err == nil {
			t.Errorf("% x: %v", data, d)
		}
	}
}

func FuzzRead(f *testing.F) {
	for _, name := range []string{"clip.mkv", "clip.mp4"} {
		if data, err := readFile("testdata/" + name); err == nil {
			f.Add(data[:min(len(data), 4096)])
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		Read(bytes.NewReader(data), int64(len(data)))
	})
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
