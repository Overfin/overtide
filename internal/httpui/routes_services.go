package httpui

import "net/http"

// servicesRoutes are the Services page's JSON API.
func (server *Server) servicesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services", server.servicesList)
	mux.HandleFunc("GET /api/services/types", server.serviceTypes)
	mux.HandleFunc("GET /api/services/consistency", server.serviceConsistencyStatus)
	mux.HandleFunc("POST /api/services/test", server.testServiceConnection)
	mux.HandleFunc("POST /api/services", server.addService)
	mux.HandleFunc("GET /api/services/{id}", server.serviceGet)
	mux.HandleFunc("PUT /api/services/{id}", server.editService)
	mux.HandleFunc("DELETE /api/services/{id}", server.removeService)
}
