package filetopology

import (
	"testing"

	"github.com/overfin/overtide/internal/model"
)

// FuzzHardlinked builds up to eight files on four inodes, each claimed by a
// film, a torrent, both or neither, with true link counts. A torrent and a
// film are hardlinked exactly when two different readable paths, one of each,
// share an inode, and both ways of asking must say so.
func FuzzHardlinked(f *testing.F) {
	f.Add([]byte{0x04, 0x08, 0x14, 0x18})
	f.Add([]byte{0x0c, 0x0c})
	f.Add([]byte{0x84, 0x08, 0x48, 0x04})
	f.Fuzz(func(t *testing.T, disk []byte) {
		names := []string{"/m/a", "/m/b", "/d/a", "/d/b", "/d/c", "/x/a", "/x/b", "/x/c"}
		var files []model.File
		var mediaRefs []model.MediaFileRef
		var torrentRefs []model.TorrentFileRef
		for i, b := range disk {
			if i == len(names) {
				break
			}
			files = append(files, model.File{
				Path:          names[i],
				SizeBytes:     100,
				Exists:        b&0x80 == 0,
				IdentityKnown: b&0x40 == 0,
				Device:        1,
				Inode:         uint64(b>>4&0x03) + 1,
			})
			if b&0x04 != 0 {
				mediaRefs = append(mediaRefs, model.MediaFileRef{MediaType: model.Movie, ServiceID: "radarr", MediaID: 1, Path: names[i]})
			}
			if b&0x08 != 0 {
				torrentRefs = append(torrentRefs, model.TorrentFileRef{Hash: "ABC", Path: names[i]})
			}
		}
		links := map[uint64]uint64{}
		for _, file := range files {
			links[file.Inode]++
		}
		for i := range files {
			files[i].Links = links[files[i].Inode]
		}

		index := New(files, mediaRefs, torrentRefs)
		hardlinked, known := index.TorrentMediaHardlink("abc", model.Movie, "radarr", 1)
		h2, k2 := index.PathsHardlinked(index.MediaPaths(model.Movie, "radarr", 1), index.TorrentPaths("abc"))
		if hardlinked != h2 || known != k2 {
			t.Fatalf("TorrentMediaHardlink (%v, %v), PathsHardlinked (%v, %v)", hardlinked, known, h2, k2)
		}

		readable := func(path string) (model.File, bool) {
			file, _ := index.File(path)
			return file, file.Exists && file.IdentityKnown
		}
		want, complete := false, len(mediaRefs) > 0 && len(torrentRefs) > 0
		for _, m := range mediaRefs {
			mf, ok := readable(m.Path)
			complete = complete && ok
			for _, q := range torrentRefs {
				qf, ok2 := readable(q.Path)
				complete = complete && ok2
				if ok && ok2 && m.Path != q.Path && mf.Inode == qf.Inode {
					want = true
				}
			}
		}
		if hardlinked != want {
			t.Fatalf("files %+v, films %v, torrents %v: hardlinked %v, want %v", files, mediaRefs, torrentRefs, hardlinked, want)
		}
		if !want && known != complete {
			t.Fatalf("files %+v, films %v, torrents %v: known %v, want %v", files, mediaRefs, torrentRefs, known, complete)
		}
		if want && !known {
			t.Fatalf("a proven hardlink must be known")
		}
	})
}
