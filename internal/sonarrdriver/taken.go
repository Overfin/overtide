package sonarrdriver

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/services/sonarr"
)

// What "Manage all" took over, each title with how it was monitored before
// Overtide changed it: so the title goes back exactly as it was when the
// operator tags it overtide-ignore, or switches back to managing only what
// Overtide added. Kept on its own, written before each change.

const takenKey = "adding.taken"

// TakenTitle is a title "Manage all" took over, as it was before: a film's
// monitoring, or a series' (Series).
type TakenTitle struct {
	Monitored bool               `json:"monitored"`
	Series    *sonarr.Monitoring `json:"series,omitempty"`
}

// TakenKey names a title in Taken: its service, kind and ID there.
func TakenKey(serviceID string, kind model.MediaType, id int) string {
	return fmt.Sprintf("%s|%s|%d", serviceID, kind, id)
}

var takenMu sync.Mutex

// Taken reads what "Manage all" took over, by TakenKey.
func (driver *Driver) Taken() (map[string]TakenTitle, error) {
	out := map[string]TakenTitle{}
	raw, err := driver.inv.Store().Meta(takenKey)
	if err != nil || raw == "" {
		return out, err
	}
	return out, json.Unmarshal([]byte(raw), &out)
}

// RecordTaken keeps how titles were monitored before Overtide takes them;
// one recorded already keeps its first record (what it was before
// Overtide ever touched it).
func (driver *Driver) RecordTaken(titles map[string]TakenTitle) error {
	return driver.changeTaken(func(taken map[string]TakenTitle) {
		for key, t := range titles {
			if _, there := taken[key]; !there {
				taken[key] = t
			}
		}
	})
}

// ForgetTaken drops titles from the record, once given back.
func (driver *Driver) ForgetTaken(keys ...string) error {
	return driver.changeTaken(func(taken map[string]TakenTitle) {
		for _, key := range keys {
			delete(taken, key)
		}
	})
}

func (driver *Driver) changeTaken(change func(map[string]TakenTitle)) error {
	takenMu.Lock()
	defer takenMu.Unlock()
	taken, err := driver.Taken()
	if err != nil {
		return err
	}
	change(taken)
	encoded, err := json.Marshal(taken)
	if err != nil {
		return err
	}
	return driver.inv.Store().SetMeta(takenKey, string(encoded))
}

// ForgetSeries drops a series from what Overtide follows and searches: its
// new episodes, its wanted episodes, its seasons (but a pack downloading,
// followed to its end).
func (driver *Driver) ForgetSeries(serviceID string, seriesID int) error {
	states, err := driver.States()
	if err != nil {
		return err
	}
	state := states[serviceID]
	if state == nil {
		return nil
	}
	delete(state.Follow, seriesID)
	for id, want := range state.Wanted {
		if want.SeriesID == seriesID && !want.Grabbed {
			delete(state.Wanted, id)
		}
	}
	for key, pack := range state.Seasons {
		if pack.SeriesID == seriesID && pack.GrabbedAt.IsZero() {
			delete(state.Seasons, key)
		}
	}
	return driver.SaveStates(states)
}
