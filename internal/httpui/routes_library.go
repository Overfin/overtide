package httpui

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
	"github.com/overfin/overtide/internal/tasks"
)

// libraryRoutes are the Library, title, History and Tasks pages' JSON API.
func (server *Server) libraryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/library", server.libraryList)
	mux.HandleFunc("GET /api/library/{type}/{id}", server.libraryTitle)
	mux.HandleFunc("GET /api/history", server.historyList)
	mux.HandleFunc("GET /api/tasks", server.tasksList)
	mux.HandleFunc("POST /api/tasks/run", server.tasksRun)
}

// humanSize is a size as the pages write it (cleanup.Human), never below 0.
func humanSize(bytes int64) string {
	if bytes < 0 {
		bytes = 0
	}
	return cleanup.Human(uint64(bytes))
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ---- Library ----

type libraryOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type libraryRowJSON struct {
	Type      string  `json:"type"`
	SourceID  int     `json:"sourceId"`
	ServiceID string  `json:"serviceId"`
	Title     string  `json:"title"`
	Year      int     `json:"year,omitempty"`
	Value     float64 `json:"value"`
	Size      string  `json:"size"`
	Torrents  int     `json:"torrents"`
	Removable bool    `json:"removable"`
}

type libraryJSON struct {
	Refreshing       bool             `json:"refreshing"`
	Error            string           `json:"error,omitempty"`
	HasMediaLibrary  bool             `json:"hasMediaLibrary"`
	HasTorrentClient bool             `json:"hasTorrentClient"`
	Total            int              `json:"total"`
	All              int              `json:"all"`
	Page             int              `json:"page"`
	PageSize         int              `json:"pageSize"`
	TotalPages       int              `json:"totalPages"`
	Sort             string           `json:"sort"`
	Order            string           `json:"order"`
	Query            string           `json:"query"`
	Type             string           `json:"type"`
	TypeOptions      []libraryOption  `json:"typeOptions"`
	Source           string           `json:"source,omitempty"`
	SourceOptions    []libraryOption  `json:"sourceOptions,omitempty"`
	Torrent          string           `json:"torrent"`
	ShowNoFiles      bool             `json:"showNoFiles"`
	Rows             []libraryRowJSON `json:"rows"`
}

// libraryList answers GET /api/library with the Library page's query
// (q, type, source, torrent, show_no_files, sort, order, page, page_size).
func (server *Server) libraryList(w http.ResponseWriter, r *http.Request) {
	d := server.libraryData(r)
	out := libraryJSON{Refreshing: d.Refreshing, Error: errText(d.LastErr), HasMediaLibrary: d.HasMediaLibrary, HasTorrentClient: d.HasTorrentClient,
		Total: d.TotalItems, All: d.AllItems, Page: d.Page, PageSize: d.PageSize, TotalPages: d.TotalPages, Sort: d.Sort, Order: d.Order,
		Query: d.Query, Type: d.TypeFilter, TypeOptions: []libraryOption{}, Source: d.SourceFilter, Torrent: d.TorrentFilter, ShowNoFiles: d.ShowNoFiles,
		Rows: []libraryRowJSON{}}
	for _, option := range d.TypeOptions {
		out.TypeOptions = append(out.TypeOptions, libraryOption(option))
	}
	for _, option := range d.SourceOptions {
		out.SourceOptions = append(out.SourceOptions, libraryOption{Value: option.Key, Label: option.Label})
	}
	for _, row := range d.Rows {
		m := row.Media
		out.Rows = append(out.Rows, libraryRowJSON{Type: string(m.Type), SourceID: m.SourceID, ServiceID: m.ServiceID, Title: m.Title, Year: m.Year,
			Value: m.RetentionValue, Size: humanSize(m.SizeBytes), Torrents: len(m.Torrents), Removable: row.Removable})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- A title ----

type torrentRowJSON struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Size        string  `json:"size"`
	Ratio       float64 `json:"ratio"`
	Seeds       int     `json:"seeds"`
	Leechers    int     `json:"leechers"`
	Reclaimable string  `json:"reclaimable,omitempty"`
}

type filePathJSON struct {
	Path  string `json:"path"`
	Short string `json:"short"`
	Owner string `json:"owner,omitempty"`
}

type fileJSON struct {
	filePathJSON
	Size          string `json:"size"`
	Exists        bool   `json:"exists"`
	IdentityKnown bool   `json:"identityKnown"`
	Links         uint64 `json:"links,omitempty"`
	// SharedWith are the file's other paths (hardlinks), when it has some.
	SharedWith []filePathJSON `json:"sharedWith,omitempty"`
}

type mediaJSON struct {
	Type        string         `json:"type"`
	SourceID    int            `json:"sourceId"`
	ServiceID   string         `json:"serviceId"`
	ServiceName string         `json:"serviceName,omitempty"`
	Title       string         `json:"title"`
	Year        int            `json:"year,omitempty"`
	Value       float64        `json:"value"`
	Reasons     []model.Reason `json:"reasons"`
	Size        string         `json:"size"`
	Path        string         `json:"path"`
	AddedAt     time.Time      `json:"addedAt,omitzero"`
	TMDBID      int            `json:"tmdbId,omitempty"`
	TVDBID      int            `json:"tvdbId,omitempty"`
	IMDBID      string         `json:"imdbId,omitempty"`
}

type mediaDetailJSON struct {
	Refreshing   bool             `json:"refreshing"`
	Error        string           `json:"error,omitempty"`
	Media        mediaJSON        `json:"media"`
	Current      []torrentRowJSON `json:"current"`
	Superseded   []torrentRowJSON `json:"superseded"`
	Orphaned     []torrentRowJSON `json:"orphaned"`
	Unassociated []torrentRowJSON `json:"unassociated"`
	Files        []fileJSON       `json:"files"`
	FileCount    int              `json:"fileCount"`
	// FilesKnown: file information was read at least once.
	FilesKnown  bool   `json:"filesKnown"`
	FilesError  string `json:"filesError,omitempty"`
	RemoveMedia string `json:"removeMedia,omitempty"`
	// RemoveWithCurrent: removing the title and its current torrents.
	RemoveWithCurrent string `json:"removeWithCurrent,omitempty"`
	KeptKey           string `json:"keptKey"`
	KeepName          string `json:"keepName"`
	Kept              bool   `json:"kept"`
	// Removable: there are files or torrents to remove.
	Removable bool `json:"removable"`
}

// operationJSON is a removal a title or torrent went through, shown instead
// of its page while it runs, or once it's gone.
type operationJSON struct {
	Operation operationNotice `json:"operation"`
}

func torrentRows(list []model.Torrent, reclaimable bool) []torrentRowJSON {
	rows := []torrentRowJSON{}
	for _, t := range list {
		row := torrentRowJSON{Hash: t.Hash, Name: t.Name, Size: humanSize(t.SizeBytes), Ratio: t.Ratio, Seeds: t.SeedsSwarm, Leechers: t.LeechersSwarm}
		if reclaimable && t.ReclaimableKnown {
			row.Reclaimable = humanSize(t.ReclaimableBytes)
		}
		rows = append(rows, row)
	}
	return rows
}

func fileRows(files []inventory.FileView) []fileJSON {
	rows := []fileJSON{}
	for _, view := range files {
		f := view.File
		row := fileJSON{filePathJSON: filePathJSON{Path: f.Path, Short: shortPath(f.Path), Owner: fileOwnerLabel(f.StorageContexts)},
			Size: humanSize(f.SizeBytes), Exists: f.Exists, IdentityKnown: f.IdentityKnown, Links: f.Links}
		if f.Exists && f.IdentityKnown && f.Links > 1 {
			for _, peer := range view.SharedWith {
				row.SharedWith = append(row.SharedWith, filePathJSON{Path: peer.Path, Short: shortPath(peer.Path), Owner: filePeerOwnerLabel(peer)})
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// libraryTitle answers GET /api/library/{type}/{id}?service_id=: a title's
// page, or the removal it went through.
func (server *Server) libraryTitle(w http.ResponseWriter, r *http.Request) {
	sourceID, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || sourceID <= 0 {
		fail(w, http.StatusNotFound, "No such title.")
		return
	}
	found := server.mediaDetail(r.PathValue("type"), sourceID, strings.TrimSpace(r.URL.Query().Get("service_id")))
	switch {
	case found.Operation != nil:
		writeJSON(w, http.StatusOK, operationJSON{Operation: *found.Operation})
		return
	case found.Status == http.StatusConflict:
		fail(w, http.StatusConflict, found.Message)
		return
	case found.Detail == nil:
		fail(w, http.StatusNotFound, "No such title.")
		return
	}
	d := found.Detail
	m := d.Media
	keepName := m.Title
	if m.Year != 0 {
		keepName += " (" + strconv.Itoa(m.Year) + ")"
	}
	out := mediaDetailJSON{Refreshing: d.Refreshing, Error: errText(d.LastErr),
		Media: mediaJSON{Type: string(m.Type), SourceID: m.SourceID, ServiceID: m.ServiceID, ServiceName: m.ServiceName, Title: m.Title, Year: m.Year,
			Value: m.RetentionValue, Reasons: m.RetentionValueReasons, Size: humanSize(m.SizeBytes), Path: m.Path, AddedAt: m.AddedAt,
			TMDBID: m.TMDBID, TVDBID: m.TVDBID, IMDBID: m.IMDBID},
		Current: torrentRows(d.Current, false), Superseded: torrentRows(d.Superseded, true), Orphaned: torrentRows(d.Orphaned, true), Unassociated: torrentRows(d.Unassociated, true),
		Files: fileRows(d.Files), FileCount: d.FileCount, FilesKnown: !d.FilesUpdated.IsZero(), FilesError: errText(d.FilesErr),
		KeptKey: d.KeptKey, KeepName: keepName, Kept: d.Kept,
		Removable: d.FileCount > 0 || len(d.Current) > 0 || len(d.Superseded) > 0 || len(d.Unassociated) > 0}
	if out.Media.Reasons == nil {
		out.Media.Reasons = []model.Reason{}
	}
	if d.RemoveMedia.Known {
		out.RemoveMedia = humanSize(d.RemoveMedia.ReclaimableBytes)
	}
	if d.RemoveWithCurrent.Known {
		out.RemoveWithCurrent = humanSize(d.RemoveWithCurrent.ReclaimableBytes)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- History ----

type historyFileJSON struct {
	Owner    string `json:"owner"`
	Selected bool   `json:"selected"`
	Path     string `json:"path"`
}

type historyEventJSON struct {
	ID          int64             `json:"id"`
	CreatedAt   time.Time         `json:"createdAt,omitzero"`
	Label       string            `json:"label"`
	Kind        string            `json:"kind"`
	ServiceName string            `json:"serviceName,omitempty"`
	DryRun      bool              `json:"dryRun"`
	Status      string            `json:"status"`
	Reclaimable string            `json:"reclaimable"`
	Error       string            `json:"error,omitempty"`
	Files       []historyFileJSON `json:"files"`
	Results     []string          `json:"results"`
	Errors      []string          `json:"errors"`
}

type cleanupRunJSON struct {
	CompletedAt     time.Time `json:"completedAt,omitzero"`
	Status          string    `json:"status"`
	UsageBefore     float64   `json:"usageBefore"`
	UsageAfter      float64   `json:"usageAfter"`
	MediaRemoved    int64     `json:"mediaRemoved"`
	TorrentsRemoved int64     `json:"torrentsRemoved"`
	Reclaimed       string    `json:"reclaimed"`
}

type historyJSON struct {
	Events []historyEventJSON `json:"events"`
	Runs   []cleanupRunJSON   `json:"runs"`
}

// historyList answers GET /api/history: the removals, newest first, and
// the legacy cleanup runs.
func (server *Server) historyList(w http.ResponseWriter, r *http.Request) {
	var runs []store.CleanupRun
	var events []store.HistoryEvent
	if db := server.inv.Store(); db != nil {
		runs, _ = db.CleanupRuns(100)
		events, _ = db.HistoryEvents(200)
	}
	out := historyJSON{Events: []historyEventJSON{}, Runs: []cleanupRunJSON{}}
	for _, view := range historyEventViews(events) {
		event := historyEventJSON{ID: view.ID, CreatedAt: view.CreatedAt, Label: view.RequestedLabel, Kind: view.RequestedKind, ServiceName: view.ServiceName,
			DryRun: view.DryRun, Status: view.Status, Reclaimable: humanSize(view.ReclaimableBytes), Error: view.Error,
			Files: []historyFileJSON{}, Results: view.Results, Errors: view.Errors}
		for _, f := range view.Files {
			event.Files = append(event.Files, historyFileJSON{Owner: string(f.Owner), Selected: f.Selected, Path: f.Path})
		}
		if event.Results == nil {
			event.Results = []string{}
		}
		if event.Errors == nil {
			event.Errors = []string{}
		}
		out.Events = append(out.Events, event)
	}
	for _, run := range runs {
		out.Runs = append(out.Runs, cleanupRunJSON{CompletedAt: run.CompletedAt, Status: run.Status, UsageBefore: run.UsageBefore, UsageAfter: run.UsageAfter,
			MediaRemoved: run.MediaRemoved, TorrentsRemoved: run.TorrentsRemoved, Reclaimed: humanSize(run.ReclaimedBytes)})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- Tasks ----

type taskJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Schedule is "Every …" or "On demand".
	Schedule     string    `json:"schedule"`
	LastFinished time.Time `json:"lastFinished,omitzero"`
	// Duration is the last run's, "—" before any.
	Duration string `json:"duration"`
	// NextRun is set only for a task that runs on a schedule.
	NextRun     time.Time `json:"nextRun,omitzero"`
	Scheduled   bool      `json:"scheduled"`
	State       string    `json:"state"`
	Priority    string    `json:"priority,omitempty"`
	Waiting     string    `json:"waiting,omitempty"`
	LastError   string    `json:"lastError,omitempty"`
	LastWarning string    `json:"lastWarning,omitempty"`
	CanRun      bool      `json:"canRun"`
	Busy        bool      `json:"busy"`
}

type taskGroupJSON struct {
	Name  string     `json:"name"`
	Tasks []taskJSON `json:"tasks"`
}

type workflowJSON struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Revision    uint64    `json:"revision"`
	State       string    `json:"state"`
	CurrentStep int       `json:"currentStep"`
	TotalSteps  int       `json:"totalSteps"`
	StepName    string    `json:"stepName,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt,omitzero"`
}

type tasksJSON struct {
	Groups    []taskGroupJSON `json:"groups"`
	Workflows []workflowJSON  `json:"workflows"`
}

func taskRow(status tasks.Status) taskJSON {
	row := taskJSON{ID: status.ID, Name: status.Name, Description: status.Description, Schedule: "On demand", LastFinished: status.LastFinished, Duration: "—",
		State: status.State, Priority: status.Priority, Waiting: status.Waiting, LastError: status.LastError, LastWarning: status.LastWarning,
		CanRun: status.CanRun, Busy: status.Running || status.Queued}
	if status.Interval > 0 {
		row.Schedule = "Every " + humanDuration(int64(status.Interval/time.Second))
		row.NextRun = status.NextRun
		row.Scheduled = true
	}
	if !status.LastFinished.IsZero() {
		row.Duration = humanDuration(int64(status.LastDuration / time.Second))
	}
	return row
}

// tasksList answers GET /api/tasks: the tasks by group, and the
// consistency workflows.
func (server *Server) tasksList(w http.ResponseWriter, r *http.Request) {
	if server.tasks == nil {
		fail(w, http.StatusServiceUnavailable, "task manager unavailable")
		return
	}
	out := tasksJSON{Groups: []taskGroupJSON{}, Workflows: []workflowJSON{}}
	for _, group := range server.taskGroups(server.tasks.Snapshot()) {
		g := taskGroupJSON{Name: group.Name, Tasks: []taskJSON{}}
		for _, status := range group.Tasks {
			g.Tasks = append(g.Tasks, taskRow(status))
		}
		out.Groups = append(out.Groups, g)
	}
	for _, flow := range server.tasks.WorkflowStatuses() {
		out.Workflows = append(out.Workflows, workflowJSON{Name: flow.Name, Description: flow.Description, Revision: flow.Revision, State: flow.State,
			CurrentStep: flow.CurrentStep, TotalSteps: flow.TotalSteps, StepName: flow.StepName, UpdatedAt: flow.UpdatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

type runTaskForm struct {
	ID string `json:"id"`
}

// tasksRun runs a task now (POST /api/tasks/run): it's queued, and the
// answer carries the trigger's id.
func (server *Server) tasksRun(w http.ResponseWriter, r *http.Request) {
	if server.tasks == nil {
		fail(w, http.StatusServiceUnavailable, "task manager unavailable")
		return
	}
	var in runTaskForm
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		fail(w, http.StatusBadRequest, "missing task id")
		return
	}
	receipt, err := server.tasks.RunAsyncApp(id)
	if err != nil {
		fail(w, http.StatusBadRequest, "task could not be scheduled: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, struct {
		TriggerID string `json:"triggerId"`
	}{receipt.TriggerID})
}
