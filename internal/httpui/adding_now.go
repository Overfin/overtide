package httpui

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// nowData is the Adding page's live panel: what Overtide is doing this
// moment, what's downloading, what holds it back, what comes next.
type nowData struct {
	// Doing is what it does now, and Detail what with; Since is how long.
	Doing   string `json:"doing"`
	Detail  string `json:"detail,omitempty"`
	Since   string `json:"since,omitempty"`
	Running bool   `json:"running"`
	// Searches is the run's searches so far, of Budget.
	Searches  int           `json:"searches"`
	Budget    int           `json:"budget"`
	DryRun    bool          `json:"dryRun"`
	Downloads []downloadRow `json:"downloads"`
	Holding   []string      `json:"holding"`
	Next      []nextRow     `json:"next"`
}

type downloadRow struct {
	Title   string `json:"title"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Done    string `json:"done,omitempty"`
	Percent int    `json:"percent"`
	Queued  bool   `json:"queued"`
}

type nextRow struct {
	Title string `json:"title"`
	Kind  string `json:"kind"`
	When  string `json:"when"`
}

// nowPanel answers GET /api/adding/now: the live panel, which the page
// asks for again every few seconds.
func (server *Server) nowPanel(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, server.nowData(r.Context()))
}

func (server *Server) nowData(ctx context.Context) nowData {
	cfg := server.inv.Config()
	now := time.Now()
	data := nowData{Downloads: []downloadRow{}, Holding: []string{}, Next: []nextRow{}}
	live := server.adder.Live()
	for _, status := range server.addingTasks() {
		if !status.Running {
			continue
		}
		data.Running = true
		data.Doing = status.Name
		if status.ID == addingTaskID && live.Running {
			data.Doing, data.DryRun = live.Phase, live.DryRun
			data.Searches, data.Budget = live.Searches, live.Budget
			if live.Title != "" {
				data.Detail = live.Title + ": " + live.Step
				data.Since = since(now.Sub(live.StepSince))
			}
		}
	}
	if !data.Running {
		data.Doing = "Waiting"
		for _, status := range server.addingTasks() {
			if status.ID == addingTaskID && !status.NextRun.IsZero() {
				data.Doing = "Waiting: titles tried next at " + status.NextRun.Local().Format("15:04")
			}
		}
	}

	switch cfg.Adding.Mode {
	case config.AddingOn:
	case config.AddingDryRun:
		data.Holding = append(data.Holding, "Dry run: nothing is added, searched for, tagged or unmonitored.")
	default:
		data.Holding = append(data.Holding, "Adding is off.")
	}
	if live.Stopped != "" && !live.Running {
		data.Holding = append(data.Holding, "The last run's searches stopped: "+live.Stopped+".")
	}
	if ok, until := server.adder.DailyLimit(1); !ok {
		data.Holding = append(data.Holding, fmt.Sprintf("The day's limit of %d titles is reached: new titles start again at %s.", cfg.Adding.PerDay(), until.Local().Format("Jan 2 15:04")))
	}
	if ok, why := server.pacer.Ready(); !ok {
		data.Holding = append(data.Holding, "No search now: "+why+".")
	}
	if server.adder.Room != nil {
		seen := map[string]bool{}
		for _, d := range cfg.Adding.Destinations {
			if seen[d.RootFolder] {
				continue
			}
			seen[d.RootFolder] = true
			if why := server.adder.Room(d.RootFolder, false); why != "" {
				data.Holding = append(data.Holding, "Nothing added to "+d.RootFolder+": "+why+".")
			}
		}
	}

	if downloads, err := server.adder.Downloads(ctx); err == nil {
		for _, d := range downloads {
			row := downloadRow{Title: d.Title, Kind: kindName(d.Kind), Status: d.Status, Percent: int(d.Progress * 100), Queued: d.Queued}
			if d.Kind == model.Series && d.Queued {
				row.Title = fmt.Sprintf("%s S%d", d.Title, d.Season)
				if d.Episodes > 1 {
					row.Status = fmt.Sprintf("%d episodes · %s", d.Episodes, d.Status)
				}
			}
			if !d.Queued {
				row.Status = "Left the queue: imported or stopped, the next run says which"
			} else if !d.Done.IsZero() && d.Done.After(now) && d.Progress < 1 {
				row.Done = "about " + since(d.Done.Sub(now)) + " left"
			}
			data.Downloads = append(data.Downloads, row)
		}
	}
	if next, err := server.adder.Next(5); err == nil {
		// When each is tried: at its own next try, the next run's at the
		// earliest, and not before the day's limit lets new titles start.
		earliest := now
		for _, status := range server.addingTasks() {
			if status.ID == addingTaskID && !status.NextRun.IsZero() && !data.Running {
				earliest = status.NextRun
			}
		}
		if ok, until := server.adder.DailyLimit(1); !ok && until.After(earliest) {
			earliest = until
		}
		for _, entry := range next {
			at := earliest
			if entry.NextTry.After(at) {
				at = entry.NextTry
			}
			row := nextRow{Title: entry.Title, Kind: kindName(entry.Kind), When: "tried " + whenWords(at, now)}
			if data.Running && !at.After(now) {
				row.When = "tried in this run, in turn"
			}
			data.Next = append(data.Next, row)
		}
	}
	return data
}

// since says a duration the way people do: 40 s, 3 min, 2 h 10 min.
func since(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

// whenWords says a time to come: "at 19:42" today, "tomorrow at 08:10",
// "Nov 12 at 08:10" after.
func whenWords(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	switch {
	case !at.After(now):
		return "now"
	case at.YearDay() == now.YearDay() && at.Year() == now.Year():
		return "at " + at.Format("15:04")
	case at.YearDay() == now.AddDate(0, 0, 1).YearDay():
		return "tomorrow at " + at.Format("15:04")
	}
	return at.Format("Jan 2") + " at " + at.Format("15:04")
}
