package httpui

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/store"
)

// historyPageSize is how many rows a page of History holds.
const historyPageSize = 100

type activityJSON struct {
	// Action is "added" or "removed".
	Action  string    `json:"action"`
	At      time.Time `json:"at"`
	Title   string    `json:"title"`
	Year    int       `json:"year,omitempty"`
	Kind    string    `json:"kind"`
	Service string    `json:"service,omitempty"`
	// Episodes are an added series' episodes, as "S4 E1–E10".
	Episodes string `json:"episodes,omitempty"`
	Files    int    `json:"files,omitempty"`
	Size     string `json:"size"`
	Origin   string `json:"origin,omitempty"`
	// A removal's status and error, and its operation.
	Status    string `json:"status,omitempty"`
	Error     string `json:"error,omitempty"`
	Operation int64  `json:"operation,omitempty"`
	// An addition's files are at /api/history/added with these.
	ServiceID string `json:"serviceId,omitempty"`
	SourceID  int    `json:"sourceId,omitempty"`
	Day       string `json:"day,omitempty"`
}

type activityPageJSON struct {
	Rows  []activityJSON   `json:"rows"`
	Total int              `json:"total"`
	Page  int              `json:"page"`
	Pages int              `json:"pages"`
	Runs  []cleanupRunJSON `json:"runs"`
}

// historyList answers GET /api/history: what was added (a title's files
// on one day) and removed, the newest first, a page of historyPageSize.
// days (7 when not given, 0 for all), show (added, removed, failed) and q
// (in the titles) choose the rows; the legacy cleanup runs come along.
func (server *Server) historyList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	days := 7
	if v := query.Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			fail(w, http.StatusBadRequest, "days must be a number of days, 0 for all")
			return
		}
		days = n
	}
	show := query.Get("show")
	if show != "" && show != "added" && show != "removed" && show != "failed" {
		fail(w, http.StatusBadRequest, "show is added, removed or failed")
		return
	}
	page, _ := strconv.Atoi(query.Get("page"))
	page = max(page, 1)
	out := activityPageJSON{Rows: []activityJSON{}, Page: page, Pages: 1, Runs: []cleanupRunJSON{}}
	db := server.inv.Store()
	if db == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	q := store.ActivityQuery{Show: show, Search: query.Get("q"), Limit: historyPageSize, Offset: (page - 1) * historyPageSize}
	if days > 0 {
		q.Since = time.Now().AddDate(0, 0, -days)
	}
	rows, total, err := db.Activities(q)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.Total, out.Pages = total, max(1, (total+historyPageSize-1)/historyPageSize)
	for _, a := range rows {
		row := activityJSON{Action: a.Action, At: a.At, Title: a.Title, Year: a.Year, Kind: a.Kind, Service: a.ServiceName,
			Episodes: episodeRanges(a.Episodes), Files: a.Files, Size: humanSize(a.Bytes), Origin: a.Origin,
			Status: a.Status, Error: a.Error, Operation: a.OperationID}
		if a.Action == "added" {
			row.ServiceID, row.SourceID, row.Day = a.ServiceID, a.SourceID, a.Day
		}
		out.Rows = append(out.Rows, row)
	}
	runs, _ := db.CleanupRuns(100)
	for _, run := range runs {
		out.Runs = append(out.Runs, cleanupRunJSON{CompletedAt: run.CompletedAt, Status: run.Status, UsageBefore: run.UsageBefore, UsageAfter: run.UsageAfter,
			MediaRemoved: run.MediaRemoved, TorrentsRemoved: run.TorrentsRemoved, Reclaimed: humanSize(run.ReclaimedBytes)})
	}
	writeJSON(w, http.StatusOK, out)
}

// episodeRanges writes episodes by season, runs shortened: "S1 E1–E3, E5 ·
// S2 E1".
func episodeRanges(episodes [][2]int) string {
	if len(episodes) == 0 {
		return ""
	}
	sorted := slices.Clone(episodes)
	slices.SortFunc(sorted, func(a, b [2]int) int {
		if a[0] != b[0] {
			return a[0] - b[0]
		}
		return a[1] - b[1]
	})
	sorted = slices.Compact(sorted)
	var seasons []string
	for i := 0; i < len(sorted); {
		season := sorted[i][0]
		var runs []string
		for i < len(sorted) && sorted[i][0] == season {
			first, last := sorted[i][1], sorted[i][1]
			for i++; i < len(sorted) && sorted[i][0] == season && sorted[i][1] == last+1; i++ {
				last = sorted[i][1]
			}
			if first == last {
				runs = append(runs, fmt.Sprintf("E%d", first))
			} else {
				runs = append(runs, fmt.Sprintf("E%d–E%d", first, last))
			}
		}
		seasons = append(seasons, fmt.Sprintf("S%d %s", season, strings.Join(runs, ", ")))
	}
	return strings.Join(seasons, " · ")
}

type importedFileJSON struct {
	At      time.Time `json:"at"`
	Episode string    `json:"episode,omitempty"`
	Path    string    `json:"path"`
	Size    string    `json:"size"`
	Release string    `json:"release,omitempty"`
}

// historyAdded answers GET /api/history/added?service=&kind=&id=&day=: a
// title's files imported that day.
func (server *Server) historyAdded(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	id, err := strconv.Atoi(query.Get("id"))
	if err != nil || query.Get("service") == "" || query.Get("kind") == "" || query.Get("day") == "" {
		fail(w, http.StatusBadRequest, "service, kind, id and day are needed")
		return
	}
	db := server.inv.Store()
	if db == nil {
		fail(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	files, err := db.ImportedFilesOn(query.Get("service"), query.Get("kind"), id, query.Get("day"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := struct {
		Files []importedFileJSON `json:"files"`
	}{Files: []importedFileJSON{}}
	for _, f := range files {
		file := importedFileJSON{At: f.ImportedAt, Path: f.Path, Size: humanSize(f.Size), Release: f.SourceTitle}
		if f.Episode > 0 {
			file.Episode = fmt.Sprintf("S%02dE%02d", f.Season, f.Episode)
		}
		out.Files = append(out.Files, file)
	}
	writeJSON(w, http.StatusOK, out)
}

// historyOperation answers GET /api/history/operations/{id}: one removal,
// dry run or not, with what it did.
func (server *Server) historyOperation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, http.StatusBadRequest, "bad operation id")
		return
	}
	db := server.inv.Store()
	if db == nil {
		fail(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	event, err := db.HistoryEventByID(id)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, historyEventOut(historyEventViews([]store.HistoryEvent{event})[0]))
}
