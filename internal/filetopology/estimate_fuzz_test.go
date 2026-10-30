package filetopology

import (
	"slices"
	"testing"

	"github.com/overfin/overtide/internal/model"
)

// FuzzEstimate builds a small disk from the input: up to eight files on
// four inodes, each link count the true number of paths to its inode, and
// some selected for removal, sometimes spelled with "./" or "x/..". Only an
// inode whose every path is selected may be counted as freed, and the order
// or spelling of the selection must not change the answer.
func FuzzEstimate(f *testing.F) {
	f.Add([]byte{0x01, 0x11, 0x02, 0x12}, uint8(0b11))
	f.Add([]byte{0x01, 0x01, 0x02, 0x00, 0x13}, uint8(0b101))
	f.Add([]byte{0x80, 0x41, 0x22}, uint8(0xff))
	f.Fuzz(func(t *testing.T, disk []byte, chosen uint8) {
		names := []string{"/m/a", "/m/b", "/d/a", "/d/b", "/d/c", "/x/a", "/x/b", "/x/c"}
		var files []model.File
		for i, b := range disk {
			if i == len(names) {
				break
			}
			files = append(files, model.File{
				Path:          names[i],
				SizeBytes:     int64(b&0x0f) * 100,
				Exists:        b&0x80 == 0,
				IdentityKnown: b&0x40 == 0,
				Device:        1,
				Inode:         uint64(b>>4&0x03) + 1,
			})
		}
		links := map[uint64]uint64{}
		for _, file := range files {
			links[file.Inode]++
		}
		for i := range files {
			files[i].Links = links[files[i].Inode]
			// All paths to one inode have its size, as stat reports.
			for _, other := range files[:i] {
				if other.Inode == files[i].Inode {
					files[i].SizeBytes = other.SizeBytes
					break
				}
			}
		}

		var plain, spelled []string
		selected := map[uint64]uint64{}
		for i, file := range files {
			if chosen&(1<<i) == 0 {
				continue
			}
			plain = append(plain, file.Path)
			spelled = append(spelled, file.Path[:2]+"/./"+file.Path[3:], file.Path[:2]+"/x/.."+file.Path[2:])
			selected[file.Inode]++
		}
		slices.Reverse(spelled)

		index := New(files, nil, nil)
		got := index.Estimate(plain)
		if again := index.Estimate(spelled); again != got {
			t.Fatalf("%v gave %+v, %v gave %+v", plain, got, spelled, again)
		}
		if got.ReclaimableBytes+got.SharedBytes != got.TotalBytes {
			t.Fatalf("freed %d + shared %d != total %d", got.ReclaimableBytes, got.SharedBytes, got.TotalBytes)
		}
		if got.Known != (len(plain) > 0 && got.UnknownFiles == 0) {
			t.Fatalf("known=%v with %d paths, %d unknown", got.Known, len(plain), got.UnknownFiles)
		}

		// Freed bytes: inodes whose paths are all readable and all selected.
		var freed int64
		counted := map[uint64]bool{}
		for _, file := range files {
			if counted[file.Inode] || selected[file.Inode] == 0 {
				continue
			}
			readable, everyPath := true, true
			for _, other := range files {
				if other.Inode != file.Inode {
					continue
				}
				pick := chosen&(1<<slices.Index(names, other.Path)) != 0
				if pick && (!other.Exists || !other.IdentityKnown) {
					readable = false
				}
				if !pick {
					everyPath = false
				}
			}
			counted[file.Inode] = true
			if readable && everyPath {
				freed += file.SizeBytes
			}
		}
		if got.UnknownFiles == 0 && got.ReclaimableBytes != freed {
			t.Fatalf("files %+v, selected %v: freed %d, want %d", files, plain, got.ReclaimableBytes, freed)
		}
		if got.ReclaimableBytes > freed {
			t.Fatalf("files %+v, selected %v: freed %d, more than the %d that can be", files, plain, got.ReclaimableBytes, freed)
		}
	})
}
