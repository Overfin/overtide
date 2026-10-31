package httpui

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
)

// torrentsRoutes are the Torrents and Unmanaged files pages' JSON API.
func (server *Server) torrentsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/torrent-list", server.apiTorrentList)
	mux.HandleFunc("GET /api/torrents/{hash}", server.apiTorrentDetail)
	mux.HandleFunc("GET /api/torrents-missing", server.apiFilesMissing)
	mux.HandleFunc("POST /api/torrents-missing", server.apiForgetMissing)
	mux.HandleFunc("GET /api/unmanaged-files", server.apiUnmanagedFiles)
}

// humanBytes is a size as every page writes it (cleanup.Human); a negative
// one is 0.
func humanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	return cleanup.Human(uint64(n))
}

// mediaLink is a title a torrent belongs (or belonged) to.
type mediaLink struct {
	Type      string `json:"type"`
	SourceID  int    `json:"sourceId"`
	ServiceID string `json:"serviceId,omitempty"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
}

func mediaLinks(refs []model.MediaRef) []mediaLink {
	out := make([]mediaLink, 0, len(refs))
	for _, ref := range refs {
		out = append(out, mediaLink{Type: string(ref.Type), SourceID: ref.SourceID, ServiceID: ref.ServiceID, Title: ref.Title, Year: ref.Year})
	}
	return out
}

// torrentRow is one line of the Torrents page, its sizes and times written
// out as the page shows them.
type torrentRow struct {
	Hash         string      `json:"hash"`
	ServiceID    string      `json:"serviceId"`
	Name         string      `json:"name"`
	Status       string      `json:"status"`
	Media        []mediaLink `json:"media"`
	FormerMedia  []mediaLink `json:"formerMedia"`
	State        string      `json:"state"`
	Progress     string      `json:"progress"`
	FilesMissing bool        `json:"filesMissing"`
	Size         string      `json:"size"`
	// Reclaimable is ReclaimableBytes written out, when known; with
	// cross-seeds, Group is what removing them together frees.
	ReclaimableKnown bool   `json:"reclaimableKnown"`
	Reclaimable      string `json:"reclaimable"`
	CrossSeeds       int    `json:"crossSeeds"`
	GroupKnown       bool   `json:"groupKnown"`
	GroupReclaimable string `json:"groupReclaimable"`
	Ratio            string `json:"ratio"`
	Seeds            int    `json:"seeds"`
	Leechers         int    `json:"leechers"`
	LastActivity     string `json:"lastActivity"`
}

func newTorrentRow(t model.Torrent) torrentRow {
	return torrentRow{
		Hash: t.Hash, ServiceID: t.ServiceID, Name: t.Name, Status: normalizeTorrentStatus(t.AssociationStatus),
		Media: mediaLinks(t.MediaItems), FormerMedia: mediaLinks(t.FormerMediaItems),
		State: t.State, Progress: fmt.Sprintf("%.1f%%", t.Progress*100), FilesMissing: t.FilesMissing, Size: humanBytes(t.SizeBytes),
		ReclaimableKnown: t.ReclaimableKnown, Reclaimable: humanBytes(t.ReclaimableBytes), CrossSeeds: len(t.SharesFilesWith),
		GroupKnown: t.GroupReclaimableKnown, GroupReclaimable: humanBytes(t.GroupReclaimableBytes),
		Ratio: fmt.Sprintf("%.2f", t.Ratio), Seeds: t.SeedsSwarm, Leechers: t.LeechersSwarm, LastActivity: unixTime(t.LastActivity),
	}
}

// pageInfo is a paged list's place: the page, its size, how many pages,
// and the sort and order understood.
type pageInfo struct {
	Page       int    `json:"page"`
	PageSize   int    `json:"pageSize"`
	TotalPages int    `json:"totalPages"`
	Sort       string `json:"sort"`
	Order      string `json:"order"`
}

type torrentListResponse struct {
	HasTorrentClient bool   `json:"hasTorrentClient"`
	Refreshing       bool   `json:"refreshing"`
	LastError        string `json:"lastError,omitempty"`
	AllItems         int    `json:"allItems"`
	TotalItems       int    `json:"totalItems"`
	Current          int    `json:"current"`
	Superseded       int    `json:"superseded"`
	Orphaned         int    `json:"orphaned"`
	Unassociated     int    `json:"unassociated"`
	// ObsoleteKnown torrents not current free ObsoleteReclaimable.
	ObsoleteKnown       int    `json:"obsoleteKnown"`
	ObsoleteReclaimable string `json:"obsoleteReclaimable"`
	FilesMissing        int    `json:"filesMissing"`
	// The filters as understood: status, reclaimable and activity in the
	// address's own words ("any" when unset).
	Query       string       `json:"q"`
	Status      string       `json:"status"`
	Reclaimable string       `json:"reclaimable"`
	Activity    string       `json:"activity"`
	Paging      pageInfo     `json:"paging"`
	Torrents    []torrentRow `json:"torrents"`
}

// apiTorrentList answers GET /api/torrent-list with the Torrents page's
// query (q, status, reclaimable, activity, sort, order, page, page_size).
func (server *Server) apiTorrentList(w http.ResponseWriter, r *http.Request) {
	d := server.torrentListing(r.URL.Query())
	out := torrentListResponse{
		HasTorrentClient: d.HasTorrentClient, Refreshing: d.Refreshing, LastError: errString(d.LastErr),
		AllItems: d.AllItems, TotalItems: d.TotalItems, Current: d.Current, Superseded: d.Superseded, Orphaned: d.Orphaned, Unassociated: d.Unassociated,
		ObsoleteKnown: d.ObsoleteKnown, ObsoleteReclaimable: humanBytes(d.ObsoleteReclaimable), FilesMissing: len(d.FilesMissing),
		Query: d.Query, Status: strings.ToLower(d.StatusFilter), Reclaimable: d.ReclaimableFilter, Activity: d.ActivityFilter,
		Paging:   pageInfo{Page: d.Page, PageSize: d.PageSize, TotalPages: d.TotalPages, Sort: d.Sort, Order: d.Order},
		Torrents: make([]torrentRow, 0, len(d.Torrents)),
	}
	for _, t := range d.Torrents {
		out.Torrents = append(out.Torrents, newTorrentRow(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// operationView is a removal under way (or done) where a page used to be:
// a torrent or title being removed shows it instead of itself.
type operationView struct {
	ID      int64  `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func newOperationView(notice operationNotice) *operationView {
	label := notice.Label
	if strings.TrimSpace(label) == "" {
		label = "Removal operation"
	}
	return &operationView{ID: notice.ID, Label: label, Status: notice.Status, Message: notice.Message}
}

// filePeerView is another path of the same physical file, and who holds it.
type filePeerView struct {
	Path  string `json:"path"`
	Short string `json:"short"`
	Owner string `json:"owner,omitempty"`
}

// fileView is one file of a torrent, as its Files section shows it.
type fileView struct {
	Path          string `json:"path"`
	Short         string `json:"short"`
	Owner         string `json:"owner,omitempty"`
	Size          string `json:"size"`
	Exists        bool   `json:"exists"`
	IdentityKnown bool   `json:"identityKnown"`
	Links         uint64 `json:"links"`
	// Peers are the file's other paths, listed only when it's hardlinked.
	Peers []filePeerView `json:"peers"`
}

func newFileView(view inventory.FileView) fileView {
	f := view.File
	out := fileView{Path: f.Path, Short: shortPath(f.Path), Owner: fileOwnerLabel(f.StorageContexts), Size: humanBytes(f.SizeBytes),
		Exists: f.Exists, IdentityKnown: f.IdentityKnown, Links: f.Links, Peers: []filePeerView{}}
	if f.Exists && f.IdentityKnown && f.Links > 1 {
		for _, peer := range view.SharedWith {
			out.Peers = append(out.Peers, filePeerView{Path: peer.Path, Short: shortPath(peer.Path), Owner: filePeerOwnerLabel(peer)})
		}
	}
	return out
}

type torrentDetailView struct {
	Hash             string      `json:"hash"`
	ServiceID        string      `json:"serviceId"`
	Name             string      `json:"name"`
	Status           string      `json:"status"`
	Client           string      `json:"client"`
	State            string      `json:"state"`
	Progress         string      `json:"progress"`
	Protected        bool        `json:"protected"`
	ProtectionReason string      `json:"protectionReason,omitempty"`
	Obligation       string      `json:"obligation,omitempty"`
	CrossSeeds       int         `json:"crossSeeds"`
	GroupKnown       bool        `json:"groupKnown"`
	GroupReclaimable string      `json:"groupReclaimable"`
	FilesMissing     bool        `json:"filesMissing"`
	ReclaimableKnown bool        `json:"reclaimableKnown"`
	Reclaimable      string      `json:"reclaimable"`
	Hardlinked       string      `json:"hardlinked,omitempty"`
	StorageError     bool        `json:"storageError"`
	Media            []mediaLink `json:"media"`
	FormerMedia      []mediaLink `json:"formerMedia"`
	SupersededByHash string      `json:"supersededByHash,omitempty"`
	Tracker          string      `json:"tracker,omitempty"`
	Category         string      `json:"category,omitempty"`
	Tags             string      `json:"tags,omitempty"`
	Ratio            string      `json:"ratio"`
	Seeds            int         `json:"seeds"`
	Leechers         int         `json:"leechers"`
	Added            string      `json:"added"`
	LastActivity     string      `json:"lastActivity"`
	SavePath         string      `json:"savePath,omitempty"`
	ContentPath      string      `json:"contentPath,omitempty"`
}

type torrentDetailResponse struct {
	// Operation, when set, is the removal under way for this torrent: the
	// page shows it instead of the torrent.
	Operation  *operationView     `json:"operation,omitempty"`
	Torrent    *torrentDetailView `json:"torrent,omitempty"`
	Refreshing bool               `json:"refreshing"`
	LastError  string             `json:"lastError,omitempty"`
	// DetailUnavailable: some details couldn't be read.
	DetailUnavailable bool `json:"detailUnavailable"`
	// Files are the first 50 of FileCount; FilesRead is false until the
	// files were read once.
	Files      []fileView `json:"files"`
	FileCount  int        `json:"fileCount"`
	FilesRead  bool       `json:"filesRead"`
	FilesError string     `json:"filesError,omitempty"`
	// RemoveKnown: removing the torrent frees RemoveFrees.
	RemoveKnown bool   `json:"removeKnown"`
	RemoveFrees string `json:"removeFrees"`
}

// apiTorrentDetail answers GET /api/torrents/{hash}?service_id=…
func (server *Server) apiTorrentDetail(w http.ResponseWriter, r *http.Request) {
	hash := strings.TrimSpace(r.PathValue("hash"))
	if hash == "" {
		fail(w, http.StatusNotFound, "No such torrent.")
		return
	}
	serviceID := strings.TrimSpace(r.URL.Query().Get("service_id"))
	if notice, pending := server.pendingProjection().torrentOperation(hash); pending {
		writeJSON(w, http.StatusOK, torrentDetailResponse{Operation: newOperationView(notice)})
		return
	}
	_, _, last := server.inv.Snapshot()
	torrent, detailErr := server.inv.TorrentDetail(hash, serviceID)
	if serviceID == "" && detailErr != nil && strings.Contains(strings.ToLower(detailErrString(detailErr)), "more than one configured instance") {
		fail(w, http.StatusConflict, detailErr.Error())
		return
	}
	if strings.Contains(strings.ToLower(detailErrString(detailErr)), "not found") {
		if notice, found := server.removalHistory("torrent", strings.ToLower(hash)); found {
			writeJSON(w, http.StatusOK, torrentDetailResponse{Operation: newOperationView(notice)})
			return
		}
		fail(w, http.StatusNotFound, "No such torrent.")
		return
	}
	storageView, filesUpdated, filesErr := server.inv.TorrentStorage(hash)
	out := torrentDetailResponse{
		Torrent:    newTorrentDetailView(torrent),
		Refreshing: server.inv.IsRefreshing(), LastError: errString(last), DetailUnavailable: detailErr != nil,
		Files: []fileView{}, FileCount: len(storageView.Files), FilesRead: !filesUpdated.IsZero(), FilesError: errString(filesErr),
		RemoveKnown: storageView.RemoveTorrent.Known, RemoveFrees: humanBytes(storageView.RemoveTorrent.ReclaimableBytes),
	}
	files := storageView.Files
	if len(files) > 50 {
		files = files[:50]
	}
	for _, f := range files {
		out.Files = append(out.Files, newFileView(f))
	}
	writeJSON(w, http.StatusOK, out)
}

func newTorrentDetailView(t model.Torrent) *torrentDetailView {
	view := &torrentDetailView{
		Hash: t.Hash, ServiceID: t.ServiceID, Name: t.Name, Status: normalizeTorrentStatus(t.AssociationStatus), Client: t.Client, State: t.State,
		Progress: fmt.Sprintf("%.1f%%", t.Progress*100), Protected: t.Protected, ProtectionReason: t.ProtectionReason, Obligation: t.Obligation,
		CrossSeeds: len(t.SharesFilesWith), GroupKnown: t.GroupReclaimableKnown, GroupReclaimable: humanBytes(t.GroupReclaimableBytes),
		FilesMissing: t.FilesMissing, ReclaimableKnown: t.ReclaimableKnown, Reclaimable: humanBytes(t.ReclaimableBytes), StorageError: t.StorageError != "",
		Media: mediaLinks(t.MediaItems), FormerMedia: mediaLinks(t.FormerMediaItems), SupersededByHash: t.SupersededByHash,
		Tracker: t.Tracker, Category: t.Category, Tags: t.Tags, Ratio: fmt.Sprintf("%.2f", t.Ratio), Seeds: t.SeedsSwarm, Leechers: t.LeechersSwarm,
		Added: unixTime(t.AddedOn), LastActivity: unixTime(t.LastActivity), SavePath: t.SavePath, ContentPath: t.ContentPath,
	}
	if t.ReclaimableKnown && t.SharedBytes > 0 {
		view.Hardlinked = humanBytes(t.SharedBytes)
	}
	return view
}

type missingTorrent struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// apiFilesMissing answers GET /api/torrents-missing: the torrents whose
// files are gone from disk, which qBittorrent still lists.
func (server *Server) apiFilesMissing(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Torrents []missingTorrent `json:"torrents"`
		DryRun   bool             `json:"dryRun"`
	}{Torrents: []missingTorrent{}, DryRun: server.inv.Config().Removal.DryRun}
	for _, t := range filesMissing(server.inv.TorrentSnapshot()) {
		out.Torrents = append(out.Torrents, missingTorrent{Name: t.Name, Status: t.AssociationStatus})
	}
	writeJSON(w, http.StatusOK, out)
}

// apiForgetMissing (POST /api/torrents-missing) removes them from
// qBittorrent, files kept (see inventory.ForgetMissingTorrents); those
// left alone are said, with why.
func (server *Server) apiForgetMissing(w http.ResponseWriter, r *http.Request) {
	var left []string
	for _, result := range server.inv.ForgetMissingTorrents(r.Context()) {
		if result.Removed {
			log.Printf("[torrents] removed from qBittorrent, files missing (dry run %t): %s", server.inv.Config().Removal.DryRun, result.Torrent.Name)
			continue
		}
		left = append(left, result.Torrent.Name+": "+result.Reason)
	}
	server.publishUIChange("torrents")
	if len(left) > 0 {
		fail(w, http.StatusConflict, "Left alone: "+strings.Join(left, "; "))
		return
	}
	done(w)
}

type unmanagedPathView struct {
	Path  string `json:"path"`
	Short string `json:"short"`
}

// unmanagedRow is one physical file of the Unmanaged files page: every
// path it has (hardlinks), which a removal takes together.
type unmanagedRow struct {
	Paths            []unmanagedPathView `json:"paths"`
	Size             string              `json:"size"`
	ReclaimableKnown bool                `json:"reclaimableKnown"`
	Reclaimable      string              `json:"reclaimable"`
	Links            uint64              `json:"links"`
	MissingLinks     int                 `json:"missingLinks"`
	Modified         string              `json:"modified"`
}

// rootOption is one storage root of the Root filter.
type rootOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type unmanagedResponse struct {
	ScanError  string `json:"scanError,omitempty"`
	AllItems   int    `json:"allItems"`
	TotalItems int    `json:"totalItems"`
	TotalBytes string `json:"totalBytes"`
	// Reclaimable and Shared are written out; empty when 0.
	Reclaimable string         `json:"reclaimable,omitempty"`
	Shared      string         `json:"shared,omitempty"`
	Query       string         `json:"q"`
	Status      string         `json:"status"`
	Root        string         `json:"root"`
	MinSizeMiB  string         `json:"minSizeMib"`
	RootOptions []rootOption   `json:"rootOptions"`
	Paging      pageInfo       `json:"paging"`
	Files       []unmanagedRow `json:"files"`
}

// apiUnmanagedFiles answers GET /api/unmanaged-files with the Unmanaged
// files page's query (q, status, root, min_size_mib, sort, order, page,
// page_size).
func (server *Server) apiUnmanagedFiles(w http.ResponseWriter, r *http.Request) {
	d := server.unmanagedListing(r.URL.Query())
	out := unmanagedResponse{
		ScanError: errString(d.ScanErr), AllItems: d.AllItems, TotalItems: d.TotalItems, TotalBytes: humanBytes(d.TotalBytes),
		Query: d.Query, Status: d.StatusFilter, Root: d.RootFilter, MinSizeMiB: d.MinSizeMiB, RootOptions: []rootOption{},
		Paging: pageInfo{Page: d.Page, PageSize: d.PageSize, TotalPages: d.TotalPages, Sort: d.Sort, Order: d.Order},
		Files:  make([]unmanagedRow, 0, len(d.Files)),
	}
	for _, option := range d.RootOptions {
		out.RootOptions = append(out.RootOptions, rootOption(option))
	}
	if d.ReclaimableBytes > 0 {
		out.Reclaimable = humanBytes(d.ReclaimableBytes)
	}
	if d.SharedBytes > 0 {
		out.Shared = humanBytes(d.SharedBytes)
	}
	for _, group := range d.Files {
		row := unmanagedRow{Size: humanBytes(group.SizeBytes), ReclaimableKnown: group.ReclaimableKnown, Reclaimable: humanBytes(group.ReclaimableBytes),
			Links: group.Links, MissingLinks: group.MissingLinks, Modified: "Never"}
		if !group.ModifiedAt.IsZero() {
			row.Modified = group.ModifiedAt.Local().Format("2006-01-02 15:04:05")
		}
		for _, file := range group.Paths {
			row.Paths = append(row.Paths, unmanagedPathView{Path: file.Path, Short: shortPath(file.Path)})
		}
		out.Files = append(out.Files, row)
	}
	writeJSON(w, http.StatusOK, out)
}
