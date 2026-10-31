package httpui

import "net/http"

// settingsRoutes are the Settings page's JSON API.
func (server *Server) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", server.settings)
	mux.HandleFunc("PUT /api/settings/password", server.changePassword)
	mux.HandleFunc("PUT /api/settings/removal", server.setRemovalSettings)
}
