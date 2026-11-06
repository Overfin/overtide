package sonarrdriver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/overfin"
	"github.com/overfin/overtide/internal/services/sonarr"
	"github.com/overfin/overtide/internal/tries"
)

// dotTorrent is a .torrent of those files.
func dotTorrent(names ...string) string {
	var files strings.Builder
	for _, name := range names {
		fmt.Fprintf(&files, "d6:lengthi1e4:pathl%d:%see", len(name), name)
	}
	return "d4:infod5:filesl" + files.String() + "e4:name1:pee"
}

func TestChooseRelease(t *testing.T) {
	tries.PollEvery = time.Millisecond // no pauses
	now := *arrfake.Day("2026-11-01")
	seasons := map[int]int{1: 2}
	episodes := sonarrEpisodes(now, seasons)
	torrents := map[string]string{
		"long":   dotTorrent("S.S01E01.mkv", "S.S01E02.mkv", "S.S01E03.mkv"),
		"fit":    dotTorrent("S.S01E01.mkv", "S.S01E02.mkv", "S.S01E01.sample.mkv", "info.nfo"),
		"titled": dotTorrent("S 1x01 - Season 1 Title 1.mkv", "S 1x02 - Season 1 Title 2.mkv", "S 1x03 - Season 1 Title 2.mkv"),
		"both":   dotTorrent("S 1x01 - Season 1 Title 1.mkv", "S 1x02 - Season 1 Title 2.mkv"),
	}
	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		body, ok := torrents[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	release := func(name string) sonarr.Release {
		return sonarr.Release{Title: name, FullSeason: true, SeasonNumber: 1, DownloadAllowed: true, DownloadURL: srv.URL + "/" + name}
	}
	driver := newTestDriver(t, config.Config{}, now)
	ctx := context.Background()
	pick := func(pack *SeasonPack, orders *overfin.EpisodeOrders, names ...string) string {
		var releases []sonarr.Release
		for _, name := range names {
			releases = append(releases, release(name))
		}
		chosen := driver.chooseRelease(ctx, releases, pack, episodes, orders)
		if chosen == nil {
			return ""
		}
		return chosen.Title
	}
	one := tmdbOrders(now, seasons)
	rearranged := tmdbOrders(now, seasons)
	rearranged.Seasons[0].Rearranged = true

	// Numbers can be believed: what fits, though Sonarr prefers another.
	if got := pick(&SeasonPack{Season: 1}, one, "long", "titled", "fit"); got != "fit" {
		t.Errorf("one order: %s", got)
	}
	// They can't: titles before what merely fits.
	if got := pick(&SeasonPack{Season: 1}, rearranged, "long", "fit", "titled"); got != "titled" {
		t.Errorf("rearranged: %s", got)
	}
	// Both: it stops reading there.
	reads = 0
	if got := pick(&SeasonPack{Season: 1}, one, "both", "fit"); got != "both" || reads != 1 {
		t.Errorf("both: %s after %d reads", got, reads)
	}
	// Always something: what doesn't suit, what can't be read.
	if got := pick(&SeasonPack{Season: 1}, one, "long"); got != "long" {
		t.Errorf("nothing suits: %s", got)
	}
	if got := pick(&SeasonPack{Season: 1}, one, "gone", "long"); got != "long" {
		t.Errorf("one unreadable: %s", got)
	}
	if got := pick(&SeasonPack{Season: 1}, one, "gone", "missing"); got != "gone" {
		t.Errorf("none readable, Sonarr's order: %s", got)
	}
	// Tried already: last, and only when nothing else is left.
	if got := pick(&SeasonPack{Season: 1, Tried: []string{"fit"}}, one, "fit", "long"); got != "long" {
		t.Errorf("tried: %s", got)
	}
	if got := pick(&SeasonPack{Season: 1, Tried: []string{"fit"}}, one, "fit"); got != "fit" {
		t.Errorf("only the tried one: %s", got)
	}
	// Refused for something else than existing files: never.
	refused := release("fit")
	refused.Rejections = []string{"Not an upgrade"}
	if chosen := driver.chooseRelease(ctx, []sonarr.Release{refused}, &SeasonPack{Season: 1}, episodes, one); chosen != nil {
		t.Errorf("refused: %s", chosen.Title)
	}
	// At most maxRead are read, each with its one retry.
	reads = 0
	var many []string
	for i := 0; i < maxRead+3; i++ {
		many = append(many, fmt.Sprintf("gone%d", i))
	}
	pick(&SeasonPack{Season: 1}, one, many...)
	if reads > maxRead*2 {
		t.Errorf("%d reads", reads)
	}
}

// A tracker's key in a .torrent's address never reaches an error.
func TestReadTorrentHidesTheAddress(t *testing.T) {
	tries.PollEvery = time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/moved":
			http.Redirect(w, r, "http://other.example/secret-key", http.StatusFound)
		case "/junk":
			fmt.Fprint(w, "not bencode")
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	for _, address := range []string{srv.URL + "/denied?apikey=secret-key", srv.URL + "/moved?apikey=secret-key", srv.URL + "/junk?apikey=secret-key", "http://127.0.0.1:1/x?apikey=secret-key", "magnet:?xt=urn:btih:secret-key"} {
		_, err := readTorrent(context.Background(), address)
		if err == nil {
			t.Errorf("no error for %s", address)
		} else if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("the address shows: %v", err)
		}
	}
}
