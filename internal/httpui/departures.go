package httpui

import (
	"context"
	"log"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/overfin"
)

// departureRounds bounds how many times one plan asks Overfin again about
// the titles that took held ones' places.
const departureRounds = 5

// heldTitle is a title Overfin holds for a last watch: someone keeps it for
// later or is partway through it. Plans skip it until then.
type heldTitle struct {
	Device string
	Title  string
	Until  time.Time
}

// departureHolds are the titles held now, by plan action key: asked about
// by automatic removal, read by every plan shown.
type departureHolds struct {
	mu    sync.Mutex
	byKey map[string]heldTitle
}

// held is a copy of what's held now, those whose date has passed left out.
func (h *departureHolds) held(now time.Time) map[string]heldTitle {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]heldTitle{}
	for key, t := range h.byKey {
		if t.Until.After(now) {
			out[key] = t
		}
	}
	return out
}

// set records what Overfin answered about these keys: held, or not.
func (h *departureHolds) set(answers map[string]heldTitle, asked []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byKey == nil {
		h.byKey = map[string]heldTitle{}
	}
	for _, key := range asked {
		if t, ok := answers[key]; ok {
			h.byKey[key] = t
		} else {
			delete(h.byKey, key)
		}
	}
}

// heldKeys adds what's held now to excluded, for a plan to skip.
func (server *Server) heldKeys(excluded map[string]bool) map[string]bool {
	held := server.holds.held(time.Now())
	if len(held) == 0 {
		return excluded
	}
	out := make(map[string]bool, len(excluded)+len(held))
	for key := range excluded {
		out[key] = true
	}
	for key := range held {
		out[key] = true
	}
	return out
}

// heldOn lists what's held on a device, the first to leave first, for its
// plan.
func (server *Server) heldOn(device string) []heldTitle {
	var out []heldTitle
	for _, t := range server.holds.held(time.Now()) {
		if t.Device == device {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Until.Equal(out[j].Until) {
			return out[i].Until.Before(out[j].Until)
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// departureOf is what Overfin knows a plan's title by: a film, a season or
// a whole series, by TMDB ID; false for a torrent, or a title without one.
func departureOf(action cleanup.Action) (overfin.Departure, bool) {
	if action.Kind == cleanup.StandaloneTorrent || action.Media.TMDBID <= 0 {
		return overfin.Departure{}, false
	}
	d := overfin.Departure{Kind: "movie", TMDB: strconv.Itoa(action.Media.TMDBID)}
	if action.Media.Type == model.Series {
		d.Kind = "series"
		if action.Season != nil {
			n := action.Season.Number
			d.Kind, d.Season = "season", &n
		}
	}
	return d, true
}

// titleOf names a plan's title for the held list.
func titleOf(action cleanup.Action) string {
	if action.Season != nil {
		return action.Media.Title + " S" + strconv.Itoa(action.Season.Number)
	}
	return action.Media.Title
}

// holdDepartures builds a device's plan, asking Overfin first which of the
// titles it would remove should wait for a last watch: those are skipped,
// and the titles taking their places are asked about in turn, a few
// rounds at most. take says which of a plan's actions would really be
// done (automatic removal leaves some out, and goes further down the list
// for them): those are the ones asked about. Nothing is asked in dry run,
// nor without Overfin; when Overfin can't answer, the holds known stay.
// The second answer lists the actions asked about (or that need no asking,
// torrents): the caller takes no other.
func (server *Server) holdDepartures(ctx context.Context, cfg config.Config, device string, build func(excluded map[string]bool) (cleanup.Plan, error), take func(cleanup.Plan) []cleanup.Action) (cleanup.Plan, map[string]bool, error) {
	client := server.inv.OverfinClient()
	ask := !cfg.Removal.DryRun && client.Configured()
	asked := map[string]overfin.Departure{}
	names := map[string]string{}
	cleared := map[string]bool{} // asked (or nothing to ask): may be taken
	var order []string
	for round := 0; ; round++ {
		plan, err := build(server.heldKeys(nil))
		if err != nil || !plan.Available {
			return plan, cleared, err
		}
		fresh := false
		for _, action := range take(plan) {
			key := cleanup.ActionKey(action)
			if _, seen := asked[key]; seen {
				continue
			}
			d, ok := departureOf(action)
			if !ok || !ask {
				cleared[key] = true // a torrent, or no Overfin to ask
				continue
			}
			asked[key], names[key] = d, titleOf(action)
			order = append(order, key)
			fresh = true
		}
		if !fresh {
			return plan, cleared, nil
		}
		titles := make([]overfin.Departure, 0, len(order))
		for _, key := range order {
			titles = append(titles, asked[key])
		}
		answers, err := client.WithContext(ctx).Departures(titles)
		if err != nil || len(answers) != len(order) {
			log.Printf("[auto-removal] asking Overfin what should wait for a last watch: %v", err)
			return plan, cleared, nil
		}
		held := map[string]heldTitle{}
		for i, key := range order {
			if until := answers[i].HoldUntil; !until.IsZero() {
				held[key] = heldTitle{Device: device, Title: names[key], Until: until}
			} else {
				cleared[key] = true
			}
		}
		server.holds.set(held, order)
		if round == departureRounds {
			// No more rounds: what took the held ones' places this time
			// wasn't asked about, and isn't cleared.
			return plan, cleared, nil
		}
	}
}
