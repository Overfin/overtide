package httpui

import (
	stdcmp "cmp"
	"errors"
	"net/http"
	"slices"
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
var addingSources = []adding.Source{adding.FromHand, adding.FromRequest, adding.FromLibrary, adding.FromOverfin}

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
	// ID names it for Remove (POST /api/adding/remove), with Removable:
	// what downloads is followed to its end, not removed.
	ID        string `json:"id"`
	Removable bool   `json:"removable"`
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
					group.Rows = append(group.Rows, addingRow{Title: r.Title, Kind: kind, Source: r.Why, When: r.When, Result: r.Result, ID: r.Ref, Removable: r.Removable})
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
			row := addingRow{Title: entry.Title, Year: entry.Year, Kind: kindName(entry.Kind), Source: string(entry.Source), When: entry.UpdatedAt, Result: entry.Result, DryRun: strings.HasPrefix(entry.Result, "Dry run"),
				ID: entry.ServiceID + "|" + entry.Key, Removable: entry.State != adding.Downloading}
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

// addingRemove answers POST /api/adding/remove: the operator takes a
// title, a season or an episode out of Adding's lists (kind: "title",
// "season", "episode", "upgrade"; id, as the list gave it), for good.
func (server *Server) addingRemove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var err error
	switch req.Kind {
	case "title":
		err = server.adder.RemoveTitle(r.Context(), req.ID)
	case "season", "episode":
		err = server.adder.Sonarr().RemoveWanted(req.Kind, req.ID)
	case "upgrade":
		err = server.adder.RemoveUpgrade(r.Context(), req.ID)
	default:
		fail(w, http.StatusBadRequest, "kind is title, season, episode or upgrade")
		return
	}
	switch {
	case errors.Is(err, adding.ErrNotInList), errors.Is(err, sonarrdriver.ErrNotWanted):
		fail(w, http.StatusNotFound, "It's no longer in the list.")
	case errors.Is(err, adding.ErrDownloading):
		fail(w, http.StatusConflict, "It's downloading: its download is followed to its end.")
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		done(w)
	}
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

type upgradeRow struct {
	ID    string  `json:"id"`
	Title string  `json:"title"`
	Kind  string  `json:"kind"`
	Why   string  `json:"why"`
	Value float64 `json:"value,omitempty"`
	// LastSearched and Next: its last upgrade search, and when it may be
	// searched again (absent: at a run with room for it).
	LastSearched time.Time `json:"lastSearched,omitzero"`
	Next         time.Time `json:"next,omitzero"`
}

type upgradesData struct {
	Rows []upgradeRow `json:"rows"`
	// Folders are the root folders' upgrades a day (0: none there) and
	// their searches of the last 24 hours.
	Folders []upgradesFolder `json:"folders"`
	// AllItems is how many wait, TotalItems how many match q; Paging, the
	// page shown, as the other lists page.
	AllItems   int      `json:"allItems"`
	TotalItems int      `json:"totalItems"`
	Q          string   `json:"q"`
	Paging     pageInfo `json:"paging"`
	// RemovalLine: titles valued this or less aren't upgraded, removal
	// having taken titles up to it lately (absent: it took none).
	RemovalLine float64 `json:"removalLine,omitempty"`
}

type upgradesFolder struct {
	Root   string `json:"root"`
	PerDay int    `json:"perDay"`
	Today  int    `json:"today"`
}

// addingUpgrades answers GET /api/adding/upgrades: what waits for an
// upgrade search, filtered by q, sorted (by default in the order they're
// searched) and paged like the other lists.
func (server *Server) addingUpgrades(w http.ResponseWriter, r *http.Request) {
	rows, err := server.adder.Upgrades(r.Context())
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	query := r.URL.Query()
	data := upgradesData{Rows: []upgradeRow{}, Folders: []upgradesFolder{}, AllItems: len(rows), Q: query.Get("q")}
	for _, day := range server.adder.Days() {
		data.Folders = append(data.Folders, upgradesFolder{Root: day.Root, PerDay: day.UpgradesPerDay, Today: day.Upgrades})
	}
	if line, ok := server.adder.RemovalLine(); ok {
		data.RemovalLine = line
	}
	all := make([]upgradeRow, 0, len(rows))
	q := strings.ToLower(strings.TrimSpace(data.Q))
	for _, row := range rows {
		if q != "" && !strings.Contains(strings.ToLower(row.Title), q) {
			continue
		}
		all = append(all, upgradeRow{ID: row.Ref, Title: row.Title, Kind: kindName(row.Kind), Why: row.Why,
			Value: row.Value, LastSearched: row.LastSearched, Next: row.Next})
	}
	sortKey, order := query.Get("sort"), strings.ToLower(query.Get("order"))
	if !slices.Contains([]string{"order", "title", "value", "last", "next"}, sortKey) {
		sortKey = "order"
	}
	if order != "asc" && order != "desc" {
		order = "asc"
		if sortKey == "value" {
			order = "desc"
		}
	}
	sortUpgrades(all, sortKey, order)
	data.TotalItems = len(all)
	size := allowedPageSize(queryValueInt(query, "page_size", 50))
	page, pages, from, to := pageOf(len(all), queryValueInt(query, "page", 1), size)
	data.Rows = append(data.Rows, all[from:to]...)
	data.Paging = pageInfo{Page: page, PageSize: size, TotalPages: pages, Sort: sortKey, Order: order}
	writeJSON(w, http.StatusOK, data)
}

// sortUpgrades sorts the upgrades: "order" keeps the order they're
// searched (reversed by desc), the others sort by their column, ties kept
// in search order.
func sortUpgrades(rows []upgradeRow, key, order string) {
	cmp := func(a, b upgradeRow) int {
		switch key {
		case "title":
			return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		case "value":
			return stdcmp.Compare(a.Value, b.Value)
		case "last":
			return a.LastSearched.Compare(b.LastSearched)
		case "next":
			return a.Next.Compare(b.Next)
		}
		return 0
	}
	if key == "order" {
		if order == "desc" {
			slices.Reverse(rows)
		}
		return
	}
	slices.SortStableFunc(rows, func(a, b upgradeRow) int {
		if order == "desc" {
			return cmp(b, a)
		}
		return cmp(a, b)
	})
}

// pageOf places a page of size among total rows: the page (kept within
// 1 and the last), how many pages, and the rows it shows, from to to.
func pageOf(total, page, size int) (int, int, int, int) {
	pages := max(1, (total+size-1)/size)
	page = min(max(page, 1), pages)
	from := min((page-1)*size, total)
	return page, pages, from, min(from+size, total)
}
