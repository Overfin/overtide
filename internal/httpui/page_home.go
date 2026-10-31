package httpui

import (
	"fmt"
	"time"

	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

type homeData struct {
	Devices              []deviceView
	Reliability          inventory.Reliability
	Updated              time.Time
	LastErr              error
	Refreshing           bool
	TotalMedia           int
	HasMovieLibrary      bool
	Movies               int
	HasSeriesLibrary     bool
	Series               int
	LibraryBytes         int64
	TotalTorrents        int
	Current              int
	Superseded           int
	Orphaned             int
	Unassociated         int
	ObsoleteReclaimable  int64
	ObsoleteKnown        int
	UnmanagedFiles       int
	UnmanagedBytes       int64
	UnmanagedReclaimable int64
	UnmanagedAvailable   bool
	UnmanagedError       string
	Services             []inventory.ServiceStatus
	Stats                store.CleanupStats
}

func (server *Server) dashboardSnapshot() homeData {
	revision := server.revisions.current().Revision
	server.homeMu.Lock()
	defer server.homeMu.Unlock()
	if server.homeRevision == revision {
		return server.homeCache
	}
	items, updated, last := server.inv.Snapshot()
	projection := server.pendingProjection()
	items = projection.filterMedia(items)
	ts := projection.filterTorrents(server.inv.TorrentSnapshot())
	reliability := server.inv.ReliabilitySnapshot()
	planningReliable := server.planningReliable(reliability)
	cfg := server.inv.Config()
	d := homeData{
		Updated: updated, LastErr: last, Refreshing: server.inv.IsRefreshing(), Reliability: reliability, TotalMedia: len(items), Devices: server.storageViews(),
		HasMovieLibrary: len(cfg.ServicesOfType("radarr")) > 0, HasSeriesLibrary: len(cfg.ServicesOfType("sonarr")) > 0,
	}
	if !planningReliable && d.LastErr == nil {
		if server.tasks != nil && server.tasks.ConsistencyPending() {
			d.LastErr = notice("Automatic removal planning paused. Post-removal synchronization is pending")
		} else {
			d.LastErr = notice(fmt.Sprintf("Automatic removal planning paused. Overfin: %s; File topology: %s", reliability.Overfin, reliability.FileModel))
		}
	}
	for _, svc := range server.inv.StatusSnapshot() {
		if svc.Configured {
			d.Services = append(d.Services, svc)
		}
	}
	for _, m := range items {
		d.LibraryBytes += m.SizeBytes
		if m.Type == model.Movie {
			d.Movies++
		} else if m.Type == model.Series {
			d.Series++
		}
	}
	d.TotalTorrents = len(ts)
	for _, t := range ts {
		switch normalizeTorrentStatus(t.AssociationStatus) {
		case model.TorrentCurrent:
			d.Current++
		case model.TorrentSuperseded:
			d.Superseded++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += reclaimableOnce(t)
			}
		case model.TorrentOrphaned:
			d.Orphaned++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += reclaimableOnce(t)
			}
		default:
			d.Unassociated++
			if t.ReclaimableKnown {
				d.ObsoleteKnown++
				d.ObsoleteReclaimable += reclaimableOnce(t)
			}
		}
	}
	ufs, unmanagedUpdated, ue := server.inv.UnmanagedSnapshot()
	ufs = projection.filterUnmanaged(ufs)
	applyUnmanagedSummary(&d, ufs, unmanagedUpdated, ue)
	if db := server.inv.Store(); db != nil {
		d.Stats, _ = db.CleanupStatistics()
	}
	server.homeRevision = revision
	server.homeCache = d
	return d
}

func applyUnmanagedSummary(d *homeData, files []model.UnmanagedFile, updated time.Time, scanErr error) {
	if scanErr != nil {
		d.UnmanagedError = scanErr.Error()
		return
	}
	if updated.IsZero() {
		// No successful scan has completed yet. This is a normal startup state,
		// not an error and not an empty authoritative result.
		return
	}
	d.UnmanagedAvailable = true
	d.UnmanagedFiles = len(files)
	for _, f := range files {
		d.UnmanagedBytes += f.SizeBytes
		d.UnmanagedReclaimable += f.ReclaimableBytes
	}
}
