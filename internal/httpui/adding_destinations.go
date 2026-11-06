package httpui

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/services/overfin"
)

// destinationRow is a destination as the Adding page lists it.
type destinationRow struct {
	Index       int    `json:"index"`
	For         string `json:"for"`
	Service     string `json:"service"`
	RootFolder  string `json:"rootFolder"`
	ProfileName string `json:"profileName"`
}

// categoryNames are Overfin's categories by ID; nil when Overfin can't
// say.
func (server *Server) categoryNames(ctx context.Context) ([]overfin.Category, map[string]string) {
	categories, err := server.inv.OverfinClient().WithContext(ctx).Categories()
	if err != nil {
		return nil, nil
	}
	names := map[string]string{}
	for _, c := range categories {
		names[c.ID] = c.Name()
	}
	return categories, names
}

// destinationRows lists the destinations, with the services' and the
// categories' names, and the profiles' (asked of each service once).
func (server *Server) destinationRows(ctx context.Context) []destinationRow {
	cfg := server.inv.Config()
	rows := []destinationRow{}
	if len(cfg.Adding.Destinations) == 0 {
		return rows
	}
	_, categories := server.categoryNames(ctx)
	profiles := map[string]map[int]string{}
	for i, d := range cfg.Adding.Destinations {
		svc := serviceByID(cfg, d.ServiceID)
		row := destinationRow{Index: i, Service: svc.Name, RootFolder: d.RootFolder, ProfileName: strconv.Itoa(d.QualityProfileID)}
		kind := "films"
		if svc.Type == "sonarr" {
			kind = "series"
		}
		row.For = "Every title (" + kind + ")"
		if d.Category != "" {
			name := categories[d.Category]
			if name == "" {
				name = "a category Overfin no longer has (" + d.Category + ")"
			}
			row.For = name + " (" + kind + ")"
		}
		if profiles[d.ServiceID] == nil {
			profiles[d.ServiceID] = map[int]string{}
			if options, err := server.inv.Options(ctx, d.ServiceID); err == nil {
				for _, p := range options.Profiles {
					profiles[d.ServiceID][p.ID] = p.Name
				}
			}
		}
		if name := profiles[d.ServiceID][d.QualityProfileID]; name != "" {
			row.ProfileName = name
		}
		rows = append(rows, row)
	}
	return rows
}

func serviceByID(cfg config.Config, id string) config.Service {
	for _, svc := range cfg.Services {
		if svc.ID == id {
			return svc
		}
	}
	return config.Service{Name: id}
}

// choice is one option of the destination form's lists.
type choice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type destinationOptions struct {
	Categories []choice `json:"categories"`
	Folders    []choice `json:"folders"`
	Profiles   []choice `json:"profiles"`
	// Unreachable names the services that couldn't be asked.
	Unreachable []string `json:"unreachable"`
	// Current is the destination's choices, for one to change.
	Current *destinationForm `json:"current,omitempty"`
}

// destinationForm is a destination as the form sends it: the folder and
// the profile as "serviceID|folder" and "serviceID|profileID".
type destinationForm struct {
	Category string `json:"category"`
	Folder   string `json:"folder"`
	Profile  string `json:"profile"`
}

// destinationOptions answers GET /api/destinations/options?index=N: every
// Radarr's and Sonarr's root folders and quality profiles, each named
// with its service (a folder and a profile of the same service make a
// destination), Overfin's categories, and destination N's choices.
func (server *Server) destinationOptions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := destinationOptions{Categories: []choice{{Value: "", Label: "Every title of its kind"}}, Folders: []choice{}, Profiles: []choice{}, Unreachable: []string{}}
	if raw := r.URL.Query().Get("index"); raw != "" {
		index, d, ok := server.destinationAt(raw)
		if !ok || index < 0 {
			fail(w, http.StatusNotFound, "This destination is gone.")
			return
		}
		data.Current = &destinationForm{Category: d.Category, Folder: d.ServiceID + "|" + d.RootFolder, Profile: d.ServiceID + "|" + strconv.Itoa(d.QualityProfileID)}
	}
	categories, _ := server.categoryNames(ctx)
	for _, c := range categories {
		data.Categories = append(data.Categories, choice{Value: c.ID, Label: c.Name()})
	}
	for _, svc := range server.inv.Config().Services {
		if svc.Type != "radarr" && svc.Type != "sonarr" {
			continue
		}
		options, err := server.inv.Options(ctx, svc.ID)
		if err != nil {
			data.Unreachable = append(data.Unreachable, svc.Name)
			continue
		}
		label := svc.Name + " (" + addingTypeLabel(svc.Type) + "): "
		for _, folder := range options.Folders {
			data.Folders = append(data.Folders, choice{Value: svc.ID + "|" + folder, Label: label + folder})
		}
		for _, p := range options.Profiles {
			data.Profiles = append(data.Profiles, choice{Value: svc.ID + "|" + strconv.Itoa(p.ID), Label: label + p.Name})
		}
	}
	writeJSON(w, http.StatusOK, data)
}

// destinationAt finds a destination by its index; "" (a new one) is -1.
func (server *Server) destinationAt(raw string) (int, config.Destination, bool) {
	if raw == "" {
		return -1, config.Destination{}, true
	}
	index, err := strconv.Atoi(raw)
	destinations := server.inv.Config().Adding.Destinations
	if err != nil || index < 0 || index >= len(destinations) {
		return 0, config.Destination{}, false
	}
	return index, destinations[index], true
}

// destinationSave adds (POST /api/destinations) or changes (PUT
// /api/destinations/{index}) a destination.
func (server *Server) destinationSave(w http.ResponseWriter, r *http.Request) {
	index, _, ok := server.destinationAt(r.PathValue("index"))
	if !ok {
		fail(w, http.StatusNotFound, "This destination is gone.")
		return
	}
	var in destinationForm
	if err := readJSON(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	folderService, folder, _ := strings.Cut(in.Folder, "|")
	profileService, profile, _ := strings.Cut(in.Profile, "|")
	profileID, _ := strconv.Atoi(profile)
	if folderService != profileService {
		fail(w, http.StatusBadRequest, "The root folder and the quality profile must be of the same Radarr or Sonarr.")
		return
	}
	d := config.Destination{Category: in.Category, ServiceID: folderService, RootFolder: folder, QualityProfileID: profileID}
	if err := server.inv.SetDestination(r.Context(), index, d); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	done(w)
}

func (server *Server) destinationRemove(w http.ResponseWriter, r *http.Request) {
	index, _, ok := server.destinationAt(r.PathValue("index"))
	if !ok || index < 0 {
		fail(w, http.StatusNotFound, "This destination is gone.")
		return
	}
	if err := server.inv.RemoveDestination(index); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	done(w)
}
