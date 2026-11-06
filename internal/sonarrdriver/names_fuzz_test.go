package sonarrdriver

import "testing"

// Release and file names come from trackers: whatever they hold, reading
// them never panics, and a season or number read is never negative but
// -1 for "none".
func FuzzFileNames(f *testing.F) {
	for _, seed := range []string{"Show.S01E02.Title.MULTI.1080p.mkv", "One Piece 783 MULTI.mkv", `dir\Show 1x05 - A title VOSTFR.mkv`, "s999e9999", ""} {
		f.Add(seed, "One Piece (1999)")
	}
	f.Fuzz(func(t *testing.T, file, series string) {
		if season := fileSeason(file); season < -1 {
			t.Fatalf("season %d", season)
		}
		if number := fileAbsolute(file, series); number < 0 {
			t.Fatalf("absolute %d", number)
		}
		_ = fileTitle(file)
		_ = comparableTitle(file)
	})
}
