package httpui

import (
	"net/http"

	"github.com/overfin/overtide/internal/sonarrdriver"
)

// importsList answers GET /api/adding/imports: the downloads with files
// waiting to be imported.
func (server *Server) importsList(w http.ResponseWriter, r *http.Request) {
	imports, err := server.adder.Sonarr().WaitingImports()
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imports": imports})
}

// importOptions answers GET /api/adding/imports/{service}/{download}: the
// series' episodes, and what Sonarr makes of each file's number.
func (server *Server) importOptions(w http.ResponseWriter, r *http.Request) {
	episodes, guesses, err := server.adder.Sonarr().ImportOptions(r.Context(), r.PathValue("service"), r.PathValue("download"))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": episodes, "guesses": guesses})
}

// importChoose answers POST /api/adding/imports/{service}/{download}: what the
// operator says of a download. The next run, started now, does it.
func (server *Server) importChoose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string           `json:"action"`
		Files  map[string][]int `json:"files"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	choice := sonarrdriver.ImportChoice{ServiceID: r.PathValue("service"), DownloadID: r.PathValue("download"), Action: body.Action, Files: body.Files}
	if err := server.adder.Sonarr().ChooseImport(choice); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := server.runAdding(addingTaskID); err != nil {
		fail(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	done(w)
}
