package httpui

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/removal"
)

// removalRoutes are the removal window's JSON API: the plan of a removal
// (what it would remove, and what may be chosen), and its execution, which
// goes through the same admission and scheduler as ever.
func (server *Server) removalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/removal/media", server.removalPlanJSON("media"))
	mux.HandleFunc("GET /api/removal/torrent", server.removalPlanJSON("torrent"))
	mux.HandleFunc("GET /api/removal/unmanaged", server.removalPlanJSON("unmanaged"))
	mux.HandleFunc("POST /api/removal/execute", server.executeRemovalJSON)
}

// removalPlanJSON answers a removal window's plan, from the same query the
// window always took (see buildRemovalFor).
func (server *Server) removalPlanJSON(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := server.buildRemovalFor(kind, r.URL.Query())
		if err != nil {
			fail(w, http.StatusNotFound, err.Error())
			return
		}
		if err := prepareRemovalData(&data); err != nil {
			fail(w, http.StatusInternalServerError, "Removal plan could not be prepared: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, removalViewOf(data))
	}
}

// removalRequest is a removal to carry out, as the window sends it: the
// same fields its form always had (removalRequest.form), so admission,
// scope checks and the scheduler see exactly what they always saw.
type removalRequest struct {
	Kind           string   `json:"kind"`
	OperationToken string   `json:"operationToken,omitempty"`
	MediaType      string   `json:"mediaType,omitempty"`
	MediaID        int      `json:"mediaId,omitempty"`
	ServiceID      string   `json:"serviceId,omitempty"`
	Hash           string   `json:"hash,omitempty"`
	Target         bool     `json:"target,omitempty"`
	ManagedFiles   []string `json:"managedFiles,omitempty"`
	Torrents       []string `json:"torrents,omitempty"`
	Paths          []string `json:"paths,omitempty"`
	UnmanagedPaths []string `json:"unmanagedPaths,omitempty"`
	ExcludeMovies  bool     `json:"excludeMovies,omitempty"`
	ExcludeSeries  bool     `json:"excludeSeries,omitempty"`
}

// form is the request as the form's fields: a field is there only when it
// was given, as a form sends only what's filled in and ticked, since the
// scope checks refuse a kind of removal any field it may not touch.
func (request removalRequest) form() url.Values {
	form := url.Values{}
	set := func(name, value string) {
		if value != "" {
			form.Set(name, value)
		}
	}
	set("kind", request.Kind)
	set("operation_token", request.OperationToken)
	set("media_type", request.MediaType)
	if request.MediaID != 0 {
		form.Set("media_id", strconv.Itoa(request.MediaID))
	}
	set("service_id", request.ServiceID)
	set("hash", request.Hash)
	if request.Target {
		form.Set("target", "1")
	}
	for name, values := range map[string][]string{"managed_file": request.ManagedFiles, "torrent": request.Torrents, "path": request.Paths, "unmanaged_path": request.UnmanagedPaths} {
		if len(values) > 0 {
			form[name] = append([]string(nil), values...)
		}
	}
	if request.ExcludeMovies {
		form.Set("exclude_movies", "1")
	}
	if request.ExcludeSeries {
		form.Set("exclude_series", "1")
	}
	return form
}

// executeRemovalJSON queues a removal (POST /api/removal/execute): 202 with
// the operation, as the form's answer always was.
func (server *Server) executeRemovalJSON(w http.ResponseWriter, r *http.Request) {
	if server.tasks == nil {
		fail(w, http.StatusServiceUnavailable, "durable scheduler unavailable")
		return
	}
	var request removalRequest
	if err := readJSON(r, &request); err != nil {
		auditRemovalRejected(0, url.Values{}, err)
		fail(w, http.StatusBadRequest, "Invalid removal request: "+err.Error())
		return
	}
	// The window's one-time token is what makes a double click, or a
	// request sent again, the same operation rather than a second one: a
	// request without it is refused.
	if strings.TrimSpace(request.OperationToken) == "" {
		auditRemovalRejected(0, request.form(), errors.New("no operation token"))
		fail(w, http.StatusBadRequest, "This removal has no operation token: open its window again.")
		return
	}
	historyID, dryRun, status, refusal := server.scheduleRemoval(request.form())
	if refusal != "" {
		fail(w, status, refusal)
		return
	}
	server.writeRemovalAccepted(w, historyID, dryRun)
}

// removalView is a removal window: what it would remove, what may be
// chosen, and the facts (Selection) the window recalculates the
// consequences from, without asking the server again.
type removalView struct {
	Kind           removal.ObjectKind `json:"kind"`
	Label          string             `json:"label"`
	DryRun         bool               `json:"dryRun"`
	OperationToken string             `json:"operationToken"`
	// What the execution names: the media, or the torrent, or the
	// unmanaged paths.
	MediaType        string   `json:"mediaType,omitempty"`
	MediaID          int      `json:"mediaId,omitempty"`
	MediaServiceID   string   `json:"mediaServiceId,omitempty"`
	Hash             string   `json:"hash,omitempty"`
	TorrentServiceID string   `json:"torrentServiceId,omitempty"`
	UnmanagedPaths   []string `json:"unmanagedPaths"`
	Warnings         []string `json:"warnings"`
	// A media's own files, by season or folder.
	ManagedSectionLabel string                `json:"managedSectionLabel,omitempty"`
	ManagedGroups       []removalViewGroup    `json:"managedGroups"`
	TorrentTarget       bool                  `json:"torrentTarget"`
	RelatedManaged      []removalViewMedia    `json:"relatedManaged"`
	RelatedGroups       []removalViewTorrents `json:"relatedGroups"`
	// Related torrents of a media, counted.
	RelatedTorrentCount   int `json:"relatedTorrentCount"`
	SelectedTorrentCount  int `json:"selectedTorrentCount"`
	PreservedTorrentCount int `json:"preservedTorrentCount"`
	// RelatedUnmanaged may be chosen in an unmanaged removal, and are only
	// shown, preserved, in a torrent's.
	RelatedUnmanaged     []removalViewPath     `json:"relatedUnmanaged"`
	FileGroups           []removalViewFile     `json:"fileGroups"`
	PathCount            int                   `json:"pathCount"`
	CanUnmonitorMovies   bool                  `json:"canUnmonitorMovies"`
	CanUnmonitorEpisodes bool                  `json:"canUnmonitorEpisodes"`
	CanExcludeMovies     bool                  `json:"canExcludeMovies"`
	CanExcludeSeries     bool                  `json:"canExcludeSeries"`
	Selection            removalSelectionModel `json:"selection"`
}

type removalViewGroup struct {
	Label      string                 `json:"label"`
	Complete   bool                   `json:"complete"`
	TotalFiles int                    `json:"totalFiles"`
	Size       string                 `json:"size"`
	Files      []removalViewGroupFile `json:"files"`
}

// removalViewGroupFile is one library file: a media's own is chosen by its
// physical file (PhysicalKey), a related media's by its key (managed_file).
type removalViewGroupFile struct {
	Key         string `json:"key"`
	PhysicalKey string `json:"physicalKey"`
	Filename    string `json:"filename"`
	Label       string `json:"label"`
	Size        string `json:"size"`
	Selected    bool   `json:"selected"`
}

type removalViewMedia struct {
	Title     string          `json:"title"`
	Year      int             `json:"year,omitempty"`
	Type      model.MediaType `json:"type"`
	SourceID  int             `json:"sourceId"`
	ServiceID string          `json:"serviceId"`
	FileCount int             `json:"fileCount"`
	// Groups are empty in a torrent's removal: its media are only shown.
	Groups []removalViewGroup `json:"groups"`
}

type removalViewTorrents struct {
	Label    string               `json:"label"`
	Torrents []removalViewTorrent `json:"torrents"`
}

type removalViewTorrent struct {
	Hash       string `json:"hash"`
	Name       string `json:"name"`
	FileCount  int    `json:"fileCount"`
	Size       string `json:"size"`
	Meta       string `json:"meta"`
	Selectable bool   `json:"selectable"`
	Selected   bool   `json:"selected"`
}

type removalViewPath struct {
	Path       string `json:"path"`
	Short      string `json:"short"`
	Size       string `json:"size"`
	Selectable bool   `json:"selectable"`
	Selected   bool   `json:"selected"`
}

type removalViewFile struct {
	Paths         []removalViewDisplayPath `json:"paths"`
	PathCount     int                      `json:"pathCount"`
	Exists        bool                     `json:"exists"`
	Size          string                   `json:"size"`
	IdentityKnown bool                     `json:"identityKnown"`
	Links         uint64                   `json:"links"`
	MissingLinks  uint64                   `json:"missingLinks"`
	Error         string                   `json:"error,omitempty"`
}

type removalViewDisplayPath struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	Short string `json:"short"`
}

func removalSize(bytes int64) string {
	if bytes < 0 {
		bytes = 0
	}
	return cleanup.Human(uint64(bytes))
}

// removalViewOf is a prepared plan (prepareRemovalData) as the window
// shows it. Nothing a kind of removal may not touch is offered: a torrent's
// related library files and paths are only shown.
func removalViewOf(data removalData) removalView {
	torrentKind := data.Plan.Kind == removal.TorrentObject
	view := removalView{
		Kind: data.Plan.Kind, Label: data.Plan.RequestedLabel, DryRun: data.Plan.DryRun, OperationToken: data.OperationToken,
		Warnings: append([]string{}, data.Plan.Warnings...), UnmanagedPaths: append([]string{}, data.UnmanagedPaths...),
		ManagedGroups: []removalViewGroup{}, RelatedManaged: []removalViewMedia{}, RelatedGroups: []removalViewTorrents{},
		RelatedUnmanaged: []removalViewPath{}, FileGroups: []removalViewFile{}, PathCount: len(data.Plan.Files),
		RelatedTorrentCount: data.RelatedTorrentCount, SelectedTorrentCount: data.SelectedTorrentCount, PreservedTorrentCount: data.PreservedTorrentCount,
		CanUnmonitorMovies: data.CanUnmonitorMovies, CanUnmonitorEpisodes: data.CanUnmonitorEpisodes, CanExcludeMovies: data.CanExcludeMovies, CanExcludeSeries: data.CanExcludeSeries,
		Selection: data.Selection,
	}
	switch data.Plan.Kind {
	case removal.MediaObject:
		view.MediaType, view.MediaID, view.MediaServiceID = data.MediaType, data.MediaID, data.MediaServiceID
	case removal.TorrentObject:
		view.Hash, view.TorrentServiceID, view.TorrentTarget = data.Hash, data.TorrentServiceID, data.TorrentTarget
	}
	if data.Plan.Kind == removal.MediaObject && data.ManagedFileCount > 0 {
		view.ManagedSectionLabel = data.ManagedSectionLabel
		view.ManagedGroups = viewGroups(data.ManagedGroups)
	}
	for _, related := range data.RelatedManaged {
		media := removalViewMedia{Title: related.Media.Title, Year: related.Media.Year, Type: related.Media.Type, SourceID: related.Media.SourceID, ServiceID: related.Media.ServiceID, FileCount: related.FileCount, Groups: []removalViewGroup{}}
		if !torrentKind {
			media.Groups = viewGroups(related.Groups)
		}
		view.RelatedManaged = append(view.RelatedManaged, media)
	}
	for _, group := range data.RelatedGroups {
		torrents := removalViewTorrents{Label: group.Label, Torrents: []removalViewTorrent{}}
		for _, related := range group.Torrents {
			torrents.Torrents = append(torrents.Torrents, removalViewTorrent{Hash: related.Torrent.Hash, Name: related.Torrent.Name, FileCount: related.FileCount, Size: removalSize(related.Torrent.SizeBytes), Meta: relatedTorrentMeta(related), Selectable: related.Selectable, Selected: related.Selected})
		}
		view.RelatedGroups = append(view.RelatedGroups, torrents)
	}
	for _, path := range data.RelatedUnmanaged {
		selectable := data.Plan.Kind == removal.UnmanagedObject
		view.RelatedUnmanaged = append(view.RelatedUnmanaged, removalViewPath{Path: path.Path, Short: shortPath(path.Path), Size: removalSize(path.SizeBytes), Selectable: selectable, Selected: selectable && path.Selected})
	}
	for _, group := range data.FileGroups {
		file := removalViewFile{Paths: []removalViewDisplayPath{}, PathCount: len(group.Paths), Exists: group.Exists, Size: removalSize(group.SizeBytes), IdentityKnown: group.IdentityKnown, Links: group.Links, MissingLinks: group.MissingLinks, Error: group.Error}
		for _, display := range group.DisplayPaths {
			file.Paths = append(file.Paths, removalViewDisplayPath{Label: display.Label, Path: display.Path, Short: shortPath(display.Text)})
		}
		view.FileGroups = append(view.FileGroups, file)
	}
	return view
}

func viewGroups(groups []managedRemovalGroup) []removalViewGroup {
	out := []removalViewGroup{}
	for _, group := range groups {
		view := removalViewGroup{Label: group.Label, Complete: group.Complete, TotalFiles: group.TotalFiles, Size: removalSize(group.SizeBytes), Files: []removalViewGroupFile{}}
		for _, file := range group.Files {
			view.Files = append(view.Files, removalViewGroupFile{Key: managedFileKey(file.Ref), PhysicalKey: file.PhysicalKey, Filename: file.Filename, Label: file.Label, Size: removalSize(file.File.SizeBytes), Selected: file.Selected})
		}
		out = append(out, view)
	}
	return out
}
