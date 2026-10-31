package httpui

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/model"
)

type torrentData struct {
	Torrents                                    []model.Torrent
	Updated                                     time.Time
	LastErr                                     error
	Refreshing                                  bool
	Current, Superseded, Orphaned, Unassociated int
	ObsoleteReclaimable                         int64
	ObsoleteKnown                               int
	TotalItems                                  int
	Page                                        int
	PageSize                                    int
	TotalPages                                  int
	HasPrev, HasNext                            bool
	PrevURL, NextURL                            string
	PageLinks                                   []navLink
	SizeLinks                                   []navLink
	Sort, Order                                 string
	SortURLs                                    map[string]string
	AllItems                                    int
	Query                                       string
	StatusFilter                                string
	ReclaimableFilter                           string
	ActivityFilter                              string
	ClearURL                                    string
	HasTorrentClient                            bool
	// FilesMissing lists the torrents whose files are gone from disk.
	FilesMissing []model.Torrent
	DryRun       bool
}

func validTorrentSort(v string) bool {
	switch v {
	case "status", "name", "media", "state", "size", "reclaimable", "ratio", "seeds", "leechers", "activity":
		return true
	}
	return false
}

func sortTorrents(items []model.Torrent, key, order string) {
	dir := 1
	if order == "desc" {
		dir = -1
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		cmp := 0
		switch key {
		case "status":
			cmp = strings.Compare(normalizeTorrentStatus(a.AssociationStatus), normalizeTorrentStatus(b.AssociationStatus))
		case "name":
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "media":
			cmp = len(a.MediaItems) - len(b.MediaItems)
		case "state":
			cmp = strings.Compare(a.State, b.State)
		case "size":
			if a.SizeBytes < b.SizeBytes {
				cmp = -1
			} else if a.SizeBytes > b.SizeBytes {
				cmp = 1
			}
		case "reclaimable":
			if a.ReclaimableBytes < b.ReclaimableBytes {
				cmp = -1
			} else if a.ReclaimableBytes > b.ReclaimableBytes {
				cmp = 1
			}
		case "ratio":
			if a.Ratio < b.Ratio {
				cmp = -1
			} else if a.Ratio > b.Ratio {
				cmp = 1
			}
		case "seeds":
			cmp = a.SeedsSwarm - b.SeedsSwarm
		case "leechers":
			cmp = a.LeechersSwarm - b.LeechersSwarm
		case "activity":
			if a.LastActivity < b.LastActivity {
				cmp = -1
			} else if a.LastActivity > b.LastActivity {
				cmp = 1
			}
		}
		if cmp == 0 {
			cmp = strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		}
		return cmp*dir < 0
	})
}

func normalizeTorrentStatusFilter(v string) string {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "CURRENT", "ASSOCIATED", "OPEN":
		return model.TorrentCurrent
	case "SUPERSEDED":
		return model.TorrentSuperseded
	case "ORPHANED":
		return model.TorrentOrphaned
	case "UNASSOCIATED", "UNMATCHED":
		return model.TorrentUnassociated
	default:
		return "ANY"
	}
}
func normalizeReclaimableFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "positive", "known", "unknown":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "any"
	}
}
func normalizeActivityFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "active", "inactive":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "any"
	}
}
func torrentActive(t model.Torrent) bool {
	return t.SeedsConnected > 0 || t.LeechersConnected > 0 || strings.Contains(strings.ToLower(t.State), "downloading") || strings.Contains(strings.ToLower(t.State), "uploading")
}
func torrentMatchesSearch(t model.Torrent, q string) bool {
	if q == "" {
		return true
	}
	hay := []string{t.Name, t.Hash, t.Tracker, t.Category, t.Tags, t.State, t.SavePath, t.ContentPath, t.Client, t.AssociationReason}
	for _, m := range t.MediaItems {
		hay = append(hay, m.Title, fmt.Sprint(m.SourceID))
	}
	for _, m := range t.FormerMediaItems {
		hay = append(hay, m.Title, fmt.Sprint(m.SourceID))
	}
	for _, v := range hay {
		if strings.Contains(strings.ToLower(v), q) {
			return true
		}
	}
	return false
}
func filterTorrents(items []model.Torrent, q, status, reclaimable, activity string) []model.Torrent {
	if status != "ANY" {
		status = normalizeTorrentStatus(status)
	}
	out := make([]model.Torrent, 0, len(items))
	for _, t := range items {
		if !torrentMatchesSearch(t, q) {
			continue
		}
		if status != "ANY" && normalizeTorrentStatus(t.AssociationStatus) != status {
			continue
		}
		switch reclaimable {
		case "known":
			if !t.ReclaimableKnown {
				continue
			}
		case "unknown":
			if t.ReclaimableKnown {
				continue
			}
		case "positive":
			if !t.ReclaimableKnown || (t.ReclaimableBytes <= 0 && t.GroupReclaimableBytes <= 0) {
				continue
			}
		}
		a := torrentActive(t)
		if activity == "active" && !a {
			continue
		}
		if activity == "inactive" && a {
			continue
		}
		out = append(out, t)
	}
	return out
}

// torrentListing is the Torrents page's data for a query: the counts, the
// filters and sort as understood, and the page of torrents asked for.
func (server *Server) torrentListing(query url.Values) torrentData {
	_, updated, last := server.inv.Snapshot()
	all := server.pendingProjection().filterTorrents(server.inv.TorrentSnapshot())
	allItems := len(all)
	counts := map[string]int{model.TorrentCurrent: 0, model.TorrentSuperseded: 0, model.TorrentOrphaned: 0, model.TorrentUnassociated: 0}
	var obsoleteReclaimable int64
	obsoleteKnown := 0
	for i := range all {
		all[i].AssociationStatus = normalizeTorrentStatus(all[i].AssociationStatus)
		counts[all[i].AssociationStatus]++
		if all[i].AssociationStatus != model.TorrentCurrent && all[i].ReclaimableKnown {
			obsoleteKnown++
			obsoleteReclaimable += reclaimableOnce(all[i])
		}
	}

	qtext := strings.ToLower(strings.TrimSpace(query.Get("q")))
	statusFilter := normalizeTorrentStatusFilter(query.Get("status"))
	reclaimableFilter := normalizeReclaimableFilter(query.Get("reclaimable"))
	activityFilter := normalizeActivityFilter(query.Get("activity"))
	all = filterTorrents(all, qtext, statusFilter, reclaimableFilter, activityFilter)

	pageSize := allowedPageSize(queryValueInt(query, "page_size", 50))
	page := queryValueInt(query, "page", 1)
	if page < 1 {
		page = 1
	}
	sortKey := query.Get("sort")
	if !validTorrentSort(sortKey) {
		sortKey = "status"
	}
	order := strings.ToLower(query.Get("order"))
	if order != "asc" && order != "desc" {
		order = "asc"
	}
	sortTorrents(all, sortKey, order)
	total := len(all)
	pages := 1
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	if page > pages {
		page = pages
	}
	from := (page - 1) * pageSize
	if from > total {
		from = total
	}
	to := from + pageSize
	if to > total {
		to = total
	}
	mk := func(pg, size int, sk, ord string) string {
		q := url.Values{}
		q.Set("page", strconv.Itoa(pg))
		q.Set("page_size", strconv.Itoa(size))
		q.Set("sort", sk)
		q.Set("order", ord)
		if qtext != "" {
			q.Set("q", qtext)
		}
		if statusFilter != "ANY" {
			q.Set("status", strings.ToLower(statusFilter))
		}
		if reclaimableFilter != "any" {
			q.Set("reclaimable", reclaimableFilter)
		}
		if activityFilter != "any" {
			q.Set("activity", activityFilter)
		}
		return "/torrents?" + q.Encode()
	}
	sortURLs := map[string]string{}
	for _, k := range []string{"status", "name", "media", "state", "size", "reclaimable", "ratio", "seeds", "leechers", "activity"} {
		no := "asc"
		if k == "size" || k == "reclaimable" || k == "ratio" || k == "seeds" || k == "leechers" || k == "activity" {
			no = "desc"
		}
		if sortKey == k {
			if order == "asc" {
				no = "desc"
			} else {
				no = "asc"
			}
		}
		sortURLs[k] = mk(1, pageSize, k, no)
	}
	sizes := []navLink{}
	for _, n := range []int{25, 50, 100, 250} {
		sizes = append(sizes, navLink{Value: n, URL: mk(1, n, sortKey, order)})
	}
	links := []navLink{}
	for pg := maxInt(1, page-2); pg <= minInt(pages, page+2); pg++ {
		links = append(links, navLink{Value: pg, URL: mk(pg, pageSize, sortKey, order)})
	}
	d := torrentData{Torrents: all[from:to], Updated: updated, LastErr: last, Refreshing: server.inv.IsRefreshing(), Current: counts[model.TorrentCurrent], Superseded: counts[model.TorrentSuperseded], Orphaned: counts[model.TorrentOrphaned], Unassociated: counts[model.TorrentUnassociated], ObsoleteKnown: obsoleteKnown, ObsoleteReclaimable: obsoleteReclaimable, TotalItems: total, AllItems: allItems, Page: page, PageSize: pageSize, TotalPages: pages, HasPrev: page > 1, HasNext: page < pages, PageLinks: links, SizeLinks: sizes, Sort: sortKey, Order: order, SortURLs: sortURLs, Query: query.Get("q"), StatusFilter: statusFilter, ReclaimableFilter: reclaimableFilter, ActivityFilter: activityFilter, ClearURL: "/torrents", HasTorrentClient: len(server.inv.Config().ServicesOfType("qbittorrent")) > 0, FilesMissing: filesMissing(server.inv.TorrentSnapshot()), DryRun: server.inv.Config().Removal.DryRun}
	if d.HasPrev {
		d.PrevURL = mk(page-1, pageSize, sortKey, order)
	}
	if d.HasNext {
		d.NextURL = mk(page+1, pageSize, sortKey, order)
	}
	return d
}

// reclaimableOnce is what removing t frees, for totals: nothing if its files
// are gone; for a cross-seed group, the whole group's figure on one of its
// torrents (the lowest hash) and nothing on the others, so it's counted once.
func reclaimableOnce(t model.Torrent) int64 {
	if t.FilesMissing {
		return 0
	}
	if len(t.SharesFilesWith) > 0 && t.GroupReclaimableKnown {
		for _, other := range t.SharesFilesWith {
			if strings.ToLower(other) < strings.ToLower(t.Hash) {
				return 0
			}
		}
		return t.GroupReclaimableBytes
	}
	return t.ReclaimableBytes
}

// filesMissing returns the torrents whose files are gone from disk.
func filesMissing(torrents []model.Torrent) []model.Torrent {
	var out []model.Torrent
	for _, t := range torrents {
		if t.FilesMissing {
			out = append(out, t)
		}
	}
	return out
}

// queryValueInt reads an integer query parameter, or fallback.
func queryValueInt(query url.Values, key string, fallback int) int {
	v, err := strconv.Atoi(query.Get(key))
	if err != nil {
		return fallback
	}
	return v
}
