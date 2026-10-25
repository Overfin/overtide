package httpui

import (
	"log"
	"net/http"
	"strings"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/config"
)

const addingTaskID = "adding"

type addingGroup struct {
	Label string
	Hint  string
	Adopt bool
	// Open groups show unfolded; Count groups show their count alone.
	Open    bool
	Count   bool
	Entries []adding.Entry
}

type addingPageData struct {
	Mode   string
	Groups []addingGroup
	Total  int
	// SelfAdding names the import lists that still add titles on their
	// own: with adding on, they can bring back what Overtide took out.
	SelfAdding []string
	Error      string
}

// addingGroups is the page's order: what's moving first, then what waits,
// then what's already there.
var addingGroups = []struct {
	State       adding.State
	Label       string
	Hint        string
	Open, Count bool
}{
	{adding.Downloading, "Downloading", "Overtide added these, and their search started a download.", true, false},
	{adding.Waiting, "Waiting", "In the order they're tried: requests first, then Overfin's picks, then lists. A try adds the title, searches for it, and takes it out again if nothing started.", true, false},
	{adding.NotReleased, "Not out yet", "Tried once they're out at home (digital or disc).", false, false},
	{adding.WantedElsewhere, "In Radarr or Sonarr, without files", "Added by hand or by a list on its own: not Overtide's. Hand them over and Overtide tries them like its own (and takes each out if nothing is found).", false, false},
	{adding.Imported, "Imported", "Overtide's, with their files.", false, false},
	{adding.InLibrary, "Already in the library", "Your lists' titles already in Radarr or Sonarr with their files, not Overtide's: nothing to do.", false, true},
}

func (server *Server) addingData() addingPageData {
	cfg := server.inv.Config()
	data := addingPageData{Mode: cfg.Adding.Mode}
	if data.Mode == "" {
		data.Mode = config.AddingOff
	}
	entries, err := server.adder.Entries()
	if err != nil {
		data.Error = err.Error()
	}
	data.Total = len(entries)
	for _, group := range addingGroups {
		view := addingGroup{Label: group.Label, Hint: group.Hint, Adopt: group.State == adding.WantedElsewhere, Open: group.Open, Count: group.Count}
		for _, entry := range entries {
			if entry.State == group.State {
				view.Entries = append(view.Entries, entry)
			}
		}
		if len(view.Entries) > 0 {
			data.Groups = append(data.Groups, view)
		}
	}
	for _, svc := range cfg.ServicesOfType("radarr") {
		if client, err := server.inv.RadarrClient(svc.ID); err == nil {
			if lists, err := client.ImportLists(); err == nil {
				for _, list := range lists {
					if list.Enabled && list.EnableAuto {
						data.SelfAdding = append(data.SelfAdding, svc.Name+": "+list.Name)
					}
				}
			}
		}
	}
	for _, svc := range cfg.ServicesOfType("sonarr") {
		if client, err := server.inv.SonarrClient(svc.ID); err == nil {
			if lists, err := client.ImportLists(); err == nil {
				for _, list := range lists {
					if list.EnableAutomaticAdd {
						data.SelfAdding = append(data.SelfAdding, svc.Name+": "+list.Name)
					}
				}
			}
		}
	}
	return data
}

func (server *Server) addingPage(w http.ResponseWriter, r *http.Request) {
	if server.adder == nil {
		http.Error(w, "adding is unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := renderTemplate(w, server.addingTpl, server.addingData()); err != nil {
		log.Printf("[http] render adding: %v", err)
	}
}

// addingPost handles the page's three forms: the mode, handing a title
// over, and running a pass now.
func (server *Server) addingPost(w http.ResponseWriter, r *http.Request) {
	if server.adder == nil {
		http.Error(w, "adding is unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	var err error
	switch r.URL.Path {
	case "/adding/mode":
		err = server.inv.SetAddingMode(r.FormValue("mode"))
	case "/adding/adopt":
		err = server.adder.Adopt(strings.TrimSpace(r.FormValue("key")))
	case "/adding/run":
		if server.tasks == nil {
			http.Error(w, "task manager unavailable", http.StatusServiceUnavailable)
			return
		}
		_, err = server.tasks.RunAsyncApp(addingTaskID)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		data := server.addingData()
		data.Error = err.Error()
		w.WriteHeader(http.StatusBadRequest)
		_ = renderTemplate(w, server.addingTpl, data)
		return
	}
	server.publishUIChange("adding")
	http.Redirect(w, r, "/adding", http.StatusSeeOther)
}
