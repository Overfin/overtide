package httpui

import (
	"net/http"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/sonarrdriver"
)

// addingRowsShown is how many titles a group shows before "Show all".
const addingRowsShown = 50

// addingSources are the waiting list's sources, in the order titles are
// tried (the panel names them).
var addingSources = []adding.Source{adding.FromRequest, adding.FromLibrary, adding.FromOverfin}

type addingSourceCount struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
}

type addingRow struct {
	Title string `json:"title"`
	Year  int    `json:"year,omitempty"`
	// Kind is "film" or "series".
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// When is the next try for what waits, the last change otherwise.
	When   time.Time `json:"when,omitzero"`
	Result string    `json:"result"`
	DryRun bool      `json:"dryRun"`
}

type addingGroup struct {
	State string      `json:"state"`
	Rows  []addingRow `json:"rows"`
	// Count is the group's titles; Rows holds the first addingRowsShown
	// unless all are asked for.
	Count int `json:"count"`
}

type addingData struct {
	Mode string `json:"mode"`
	// Manage is config.ManageOwn or config.ManageAll.
	Manage string `json:"manage"`
	// Searches is how many searches a run may ask for.
	Searches int           `json:"searches"`
	Groups   []addingGroup `json:"groups"`
	// Sources counts the titles of each source there is.
	Sources []addingSourceCount `json:"sources"`
	Total   int                 `json:"total"`
	// Matching counts what the filters keep.
	Matching int `json:"matching"`
	// NextRead and NextRun are when the suggestions are read and the
	// titles tried next.
	NextRead time.Time `json:"nextRead,omitzero"`
	NextRun  time.Time `json:"nextRun,omitzero"`
	// LastError is what stopped the last run, if anything did.
	LastError string `json:"lastError,omitempty"`
}

// addingStates is the page's order for the titles, after the episodes and
// seasons: what waits, then what's already there. What's downloading has the
// live panel's card.
var addingStates = []adding.State{adding.Waiting, adding.NotReleased, adding.Imported}

// addingList answers GET /api/adding: the titles by state, filtered by
// q (in the title) and source; all names a state to list whole.
func (server *Server) addingList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	cfg := server.inv.Config()
	data := addingData{Mode: cfg.Adding.Mode, Manage: cfg.Adding.Manage, Groups: []addingGroup{}, Sources: []addingSourceCount{}}
	if data.Mode == "" {
		data.Mode = config.AddingOff
	}
	if data.Manage == "" {
		data.Manage = config.ManageOwn
	}
	data.Searches = cfg.Adding.SearchesPerRun()
	for _, status := range server.addingTasks() {
		switch status.ID {
		case addingOverfinTaskID:
			data.NextRead = status.NextRun
		case addingTaskID:
			data.NextRun = status.NextRun
			data.LastError = status.LastError
		}
	}
	entries, err := server.adder.Entries()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	data.Total = len(entries)
	counts := map[adding.Source]int{}
	for _, entry := range entries {
		counts[entry.Source]++
	}
	for _, source := range addingSources {
		if counts[source] > 0 {
			data.Sources = append(data.Sources, addingSourceCount{Source: string(source), Count: counts[source]})
		}
	}
	needle := strings.ToLower(strings.TrimSpace(query.Get("q")))
	source := query.Get("source")
	var kept []adding.Entry
	for _, entry := range entries {
		if source != "" && string(entry.Source) != source {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(entry.Title), needle) {
			continue
		}
		kept = append(kept, entry)
	}
	data.Matching = len(kept)
	showAll := query.Get("all")
	// Episodes and seasons have no source of the list's: a filter on one
	// leaves them out.
	if source == "" {
		episodes, seasons, err := server.adder.Sonarr().Wanted()
		if err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
		addWanted := func(state, kind string, rows []sonarrdriver.WantedRow) {
			group := addingGroup{State: state, Rows: []addingRow{}}
			for _, r := range rows {
				if needle != "" && !strings.Contains(strings.ToLower(r.Title), needle) {
					continue
				}
				group.Count++
				if group.Count <= addingRowsShown || showAll == state {
					group.Rows = append(group.Rows, addingRow{Title: r.Title, Kind: kind, Source: r.Why, When: r.When, Result: r.Result})
				}
			}
			if group.Count > 0 {
				data.Groups = append(data.Groups, group)
			}
		}
		addWanted("seasons", "season", seasons)
		addWanted("episodes", "episode", episodes)
	}
	for _, state := range addingStates {
		group := addingGroup{State: string(state), Rows: []addingRow{}}
		for _, entry := range kept {
			if entry.State != state {
				continue
			}
			group.Count++
			if group.Count > addingRowsShown && showAll != string(state) {
				continue
			}
			row := addingRow{Title: entry.Title, Year: entry.Year, Kind: kindName(entry.Kind), Source: string(entry.Source), When: entry.UpdatedAt, Result: entry.Result, DryRun: strings.HasPrefix(entry.Result, "Dry run")}
			if entry.State == adding.Waiting || entry.State == adding.NotReleased {
				row.When = entry.NextTry
			}
			group.Rows = append(group.Rows, row)
		}
		if group.Count > 0 {
			data.Groups = append(data.Groups, group)
		}
	}
	writeJSON(w, http.StatusOK, data)
}

// addingRun reads the suggestions, then tries the titles whose turn it
// is: they run one after the other.
func (server *Server) addingRun(w http.ResponseWriter, r *http.Request) {
	if err := server.runAdding(addingOverfinTaskID, addingTaskID); err != nil {
		fail(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	done(w)
}

// addingClear empties the waiting list (see adding.Service.Clear).
func (server *Server) addingClear(w http.ResponseWriter, r *http.Request) {
	if _, err := server.adder.Clear(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	done(w)
}

func kindName(kind model.MediaType) string {
	if kind == model.Series {
		return "series"
	}
	return "film"
}
