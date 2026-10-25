package httpui

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

const addingTaskID = "adding"

// addingRowsShown is how many titles a group shows before "Show all".
const addingRowsShown = 50

// addingSources are the waiting list's sources, in the order titles are
// tried, with how the page names them.
var addingSources = []struct {
	Source adding.Source
	Label  string
}{
	{adding.FromRequest, "Requests"},
	{adding.FromOverfin, "Overfin's picks"},
	{adding.FromDiscovery, "Discovery"},
	{adding.HandedOver, "Handed over"},
	{adding.FromList, "Import lists"},
}

type addingSourceCount struct {
	Value string
	Label string
	Count int
}

type addingRow struct {
	Title  string
	Year   int
	Kind   string
	Source string
	// When is the next try for what waits, the last change otherwise.
	When   time.Time
	Result string
	DryRun bool
}

type addingGroup struct {
	Label string
	Hint  string
	// WhenLabel heads the time column.
	WhenLabel string
	// Open groups show unfolded.
	Open bool
	Rows []addingRow
	// Count is the group's titles; Rows holds the first addingRowsShown
	// unless all are asked for.
	Count   int
	MoreURL string
}

type addingPageData struct {
	Mode string
	// DiscoveryShare is the part, in percent, of Overfin's suggestions
	// from TMDB's trending and popular titles.
	DiscoveryShare int
	Groups         []addingGroup
	// Sources counts the titles of each source there is.
	Sources []addingSourceCount
	Total   int
	// Query and Source filter the lists; Matching counts what they keep.
	Query    string
	Source   string
	Matching int
	NextRun  time.Time
	// WithoutFiles counts what Radarr and Sonarr have without files that
	// isn't Overtide's: what "Hand them all over" takes.
	WithoutFiles int
	Error        string
}

// addingGroups is the page's order: what's moving first, then what waits,
// then what's already there.
var addingGroups = []struct {
	State     adding.State
	Label     string
	Hint      string
	WhenLabel string
	Open      bool
}{
	{adding.Downloading, "Downloading", "Overtide added these, and their search started a download.", "Since", true},
	{adding.Waiting, "Waiting", "In the order they're tried: requests, then Overfin's suggestions, then the rest.", "Next try", true},
	{adding.NotReleased, "Not out yet", "Tried once they're out at home (digital or disc).", "Out", false},
	{adding.Imported, "Imported", "Overtide's, with their files.", "Since", false},
}

func addingSourceLabel(source adding.Source) string {
	for _, known := range addingSources {
		if known.Source == source {
			return known.Label
		}
	}
	return string(source)
}

func (server *Server) addingData(query url.Values) addingPageData {
	cfg := server.inv.Config()
	data := addingPageData{Mode: cfg.Adding.Mode, DiscoveryShare: cfg.Adding.Discovery(), WithoutFiles: server.adder.WithoutFiles(), Query: strings.TrimSpace(query.Get("q")), Source: query.Get("source")}
	if data.Mode == "" {
		data.Mode = config.AddingOff
	}
	if server.tasks != nil {
		for _, status := range server.tasks.Snapshot() {
			if status.ID == addingTaskID {
				data.NextRun = status.NextRun
			}
		}
	}
	entries, err := server.adder.Entries()
	if err != nil {
		data.Error = err.Error()
	}
	data.Total = len(entries)
	counts := map[adding.Source]int{}
	for _, entry := range entries {
		counts[entry.Source]++
	}
	for _, source := range addingSources {
		if counts[source.Source] > 0 {
			data.Sources = append(data.Sources, addingSourceCount{Value: string(source.Source), Label: source.Label, Count: counts[source.Source]})
		}
	}
	needle := strings.ToLower(data.Query)
	var kept []adding.Entry
	for _, entry := range entries {
		if data.Source != "" && string(entry.Source) != data.Source {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(entry.Title), needle) {
			continue
		}
		kept = append(kept, entry)
	}
	data.Matching = len(kept)
	showAll := query.Get("all")
	for _, group := range addingGroups {
		view := addingGroup{Label: group.Label, Hint: group.Hint, WhenLabel: group.WhenLabel, Open: group.Open}
		for _, entry := range kept {
			if entry.State != group.State {
				continue
			}
			view.Count++
			if view.Count > addingRowsShown && showAll != string(group.State) {
				continue
			}
			row := addingRow{Title: entry.Title, Year: entry.Year, Kind: "Film", Source: addingSourceLabel(entry.Source), When: entry.UpdatedAt, Result: entry.Result, DryRun: strings.HasPrefix(entry.Result, "Dry run")}
			if entry.Kind == model.Series {
				row.Kind = "Series"
			}
			if entry.From != "" && entry.Source != adding.HandedOver && entry.Source != adding.FromOverfin {
				row.Source += " · " + entry.From
			}
			if entry.State == adding.Waiting || entry.State == adding.NotReleased {
				row.When = entry.NextTry
			}
			view.Rows = append(view.Rows, row)
		}
		if view.Count > len(view.Rows) {
			more := url.Values{"all": {string(group.State)}}
			if data.Query != "" {
				more.Set("q", data.Query)
			}
			if data.Source != "" {
				more.Set("source", data.Source)
			}
			view.MoreURL = "/adding?" + more.Encode()
		}
		if view.Count > 0 {
			data.Groups = append(data.Groups, view)
		}
	}
	return data
}

func (server *Server) addingPage(w http.ResponseWriter, r *http.Request) {
	if server.adder == nil {
		http.Error(w, "adding is unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := renderTemplate(w, server.addingTpl, server.addingData(r.URL.Query())); err != nil {
		log.Printf("[http] render adding: %v", err)
	}
}

// addingPost handles the page's three forms: the mode, handing titles
// over, and running a pass now.
func (server *Server) addingPost(w http.ResponseWriter, r *http.Request) {
	if server.adder == nil {
		http.Error(w, "adding is unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	var err error
	switch r.URL.Path {
	case "/adding/mode":
		err = server.inv.SetAddingMode(r.FormValue("mode"))
		if raw := r.FormValue("discovery_share"); err == nil && raw != "" {
			share, convErr := strconv.Atoi(raw)
			if convErr != nil {
				err = fmt.Errorf("discovery share must be a whole number from 0 to 100")
			} else {
				err = server.inv.SetDiscoveryShare(share)
			}
		}
	case "/adding/hand-over":
		_, err = server.adder.HandOver()
	case "/adding/run":
		if server.tasks == nil {
			http.Error(w, "task manager unavailable", http.StatusServiceUnavailable)
			return
		}
		_, err = server.tasks.RunAsyncApp(addingTaskID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		data := server.addingData(nil)
		data.Error = err.Error()
		w.WriteHeader(http.StatusBadRequest)
		_ = renderTemplate(w, server.addingTpl, data)
		return
	}
	server.publishUIChange("adding")
	http.Redirect(w, r, "/adding", http.StatusSeeOther)
}
