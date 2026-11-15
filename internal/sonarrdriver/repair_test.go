package sonarrdriver

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// mkvLasting writes the headers of a Matroska file lasting minutes: all
// the length rule reads.
func mkvLasting(t *testing.T, file string, minutes float64) {
	t.Helper()
	duration := make([]byte, 8)
	binary.BigEndian.PutUint64(duration, math.Float64bits(minutes*60_000))                      // ticks of a millisecond
	data := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x80}                                                // EBML, empty
	data = append(data, 0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF) // Segment, size unknown
	data = append(data, 0x15, 0x49, 0xA9, 0x66, 0x8B, 0x44, 0x89, 0x88)                         // Info (11 bytes): Duration, 8 bytes
	data = append(data, duration...)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A season imported one segment a file, its files 22 minutes long and its
// segments 11: each file's torrent twin is imported again with its two
// segments, as a copy (a hardlink), in one command. A file without a twin
// repairs nothing.
func TestRepairSeason(t *testing.T) {
	now := *arrfake.Day("2026-11-15")
	fake := arrfake.New(now)
	old := now.AddDate(-1, 0, 0)
	library := t.TempDir()
	fake.AddSeries(1, nil, arrfake.Season(1, 2, 4))
	fake.Series[1]["path"], fake.Series[1]["tmdbId"] = library, 77
	var tmdb []overfin.OrderEpisode
	for n := 1; n <= 4; n++ {
		id := 100 + n
		fake.AddEpisode(id, 1, 1, old, n <= 2)
		title := fmt.Sprintf("Segment %c", 'A'+n-1)
		fake.Eps[id]["episodeNumber"], fake.Eps[id]["title"] = n, title
		tmdb = append(tmdb, overfin.OrderEpisode{Episode: n, Name: title, AirDate: old.Format("2006-01-02"), Runtime: 11})
	}
	for n := 1; n <= 2; n++ {
		relative := fmt.Sprintf("Season 1/Show - S01E%02d.mkv", n)
		mkvLasting(t, filepath.Join(library, relative), 22)
		fake.Files[n] = map[string]any{"id": n, "seriesId": 1, "seasonNumber": 1, "relativePath": relative, "size": 1, "dateAdded": arrfake.ISO(old)}
		fake.Eps[100+n]["episodeFileId"] = n
	}
	quality := map[string]any{"quality": map[string]any{"id": 3}}
	fake.ImportAt = []map[string]any{
		{"path": "/dl/Saison 1/Show.S01E01.mkv", "seasonNumber": 1, "episodes": []map[string]any{{"id": 101}}, "quality": quality, "languages": []any{}},
		{"path": "/dl/Saison 1/Show.S01E02.mkv", "seasonNumber": 1, "episodes": []map[string]any{{"id": 102}}, "quality": quality, "languages": []any{}},
		{"path": "/dl/Saison 1/Other.S01E01.mkv", "seasonNumber": 1, "episodes": []map[string]any{{"id": 101}}, "quality": quality, "languages": []any{}},
	}
	driver, _ := testDriver(t, fake, config.ManageAll)
	driver.knowOrders(77, &overfin.EpisodeOrders{Seasons: []overfin.OrderSeason{{Season: 1, Episodes: tmdb}}})
	twins := map[string]torrentTwin{
		filepath.Join(library, "Season 1/Show - S01E01.mkv"): {Path: "/dl/Saison 1/Show.S01E01.mkv", Hash: "h"},
		filepath.Join(library, "Season 1/Show - S01E02.mkv"): {Path: "/dl/Saison 1/Show.S01E02.mkv", Hash: "h"},
	}
	driver.twinsOf = func([]string) map[string]torrentTwin { return twins }
	client, err := driver.inv.SonarrClient("s")
	if err != nil {
		t.Fatal(err)
	}
	episodes, err := client.Episodes(1)
	if err != nil {
		t.Fatal(err)
	}
	series := sonarr.Title{ID: 1, Title: "Show", Path: library, TMDBID: 77}

	// In dry run: seen, nothing imported.
	if repaired, several := driver.repairSeason(context.Background(), client, series, 1, episodes, true); repaired || !several || len(fake.Imported) != 0 {
		t.Fatalf("dry run: %v %v %v", repaired, several, fake.Imported)
	}
	repaired, several := driver.repairSeason(context.Background(), client, series, 1, episodes, false)
	if !repaired || !several || len(fake.Imported) != 1 {
		t.Fatalf("repair: %v %v %v", repaired, several, fake.Imported)
	}
	if got := fake.ImportedTo["/dl/Saison 1/Show.S01E01.mkv"]; !slices.Equal(got, []int{101, 102}) {
		t.Errorf("first file: %v", got)
	}
	if got := fake.ImportedTo["/dl/Saison 1/Show.S01E02.mkv"]; !slices.Equal(got, []int{103, 104}) {
		t.Errorf("second file: %v", got)
	}
	if _, other := fake.ImportedTo["/dl/Saison 1/Other.S01E01.mkv"]; other {
		t.Error("another series' file in the folder was imported")
	}

	// A file without a twin: nothing.
	delete(twins, filepath.Join(library, "Season 1/Show - S01E02.mkv"))
	fake.Imported = nil
	if repaired, several := driver.repairSeason(context.Background(), client, series, 1, episodes, false); repaired || !several || len(fake.Imported) != 0 {
		t.Errorf("without a twin: %v %v %v", repaired, several, fake.Imported)
	}
}
