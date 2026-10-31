package httpui

import (
	"net/http"
	"time"
)

// homeRoutes are the Home page's JSON API: the disks, the library, the
// torrents, the lifetime statistics and the services, at a glance.
func (server *Server) homeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/home", server.apiHome)
}

type homeServiceJSON struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
}

type homeStatsJSON struct {
	ReclaimedBytes  int64 `json:"reclaimedBytes"`
	Runs            int64 `json:"runs"`
	MediaRemoved    int64 `json:"mediaRemoved"`
	TorrentsRemoved int64 `json:"torrentsRemoved"`
	MediaBytes      int64 `json:"mediaBytes"`
	Last30Bytes     int64 `json:"last30Bytes"`
}

type homeJSON struct {
	// Error is what holds planning back, or the last inventory error.
	Error               string              `json:"error,omitempty"`
	Updated             time.Time           `json:"updated,omitzero"`
	Refreshing          bool                `json:"refreshing"`
	Devices             []storageDeviceJSON `json:"devices"`
	TotalMedia          int                 `json:"totalMedia"`
	HasMovieLibrary     bool                `json:"hasMovieLibrary"`
	Movies              int                 `json:"movies"`
	HasSeriesLibrary    bool                `json:"hasSeriesLibrary"`
	Series              int                 `json:"series"`
	LibraryBytes        int64               `json:"libraryBytes"`
	TotalTorrents       int                 `json:"totalTorrents"`
	Current             int                 `json:"current"`
	Superseded          int                 `json:"superseded"`
	Orphaned            int                 `json:"orphaned"`
	Unassociated        int                 `json:"unassociated"`
	ObsoleteKnown       int                 `json:"obsoleteKnown"`
	ObsoleteReclaimable int64               `json:"obsoleteReclaimable"`
	UnmanagedError      string              `json:"unmanagedError,omitempty"`
	Services            []homeServiceJSON   `json:"services"`
	Stats               homeStatsJSON       `json:"stats"`
}

// homeJSONFrom turns the dashboard's data into Home's answer: the disks
// without their cleanup plans (those are the Storage page's).
func homeJSONFrom(d homeData) homeJSON {
	out := homeJSON{
		Updated: d.Updated, Refreshing: d.Refreshing, Devices: []storageDeviceJSON{}, TotalMedia: d.TotalMedia,
		HasMovieLibrary: d.HasMovieLibrary, Movies: d.Movies, HasSeriesLibrary: d.HasSeriesLibrary, Series: d.Series, LibraryBytes: d.LibraryBytes,
		TotalTorrents: d.TotalTorrents, Current: d.Current, Superseded: d.Superseded, Orphaned: d.Orphaned, Unassociated: d.Unassociated,
		ObsoleteKnown: d.ObsoleteKnown, ObsoleteReclaimable: d.ObsoleteReclaimable, UnmanagedError: d.UnmanagedError, Services: []homeServiceJSON{},
		Stats: homeStatsJSON{ReclaimedBytes: d.Stats.ReclaimedBytes, Runs: d.Stats.Runs, MediaRemoved: d.Stats.MediaRemoved,
			TorrentsRemoved: d.Stats.TorrentsRemoved, MediaBytes: d.Stats.MediaBytes, Last30Bytes: d.Stats.Last30Bytes},
	}
	if d.LastErr != nil {
		out.Error = d.LastErr.Error()
	}
	for _, view := range d.Devices {
		out.Devices = append(out.Devices, deviceJSON(view, false))
	}
	for _, svc := range d.Services {
		out.Services = append(out.Services, homeServiceJSON{Name: svc.Name, OK: svc.OK})
	}
	return out
}

func (server *Server) apiHome(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, homeJSONFrom(server.dashboardSnapshot()))
}
