package httpui

import (
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net/http"
)

// maxJSONBody is the most a JSON request body may hold: every form is a
// few fields, a removal's selection a few hundred paths at most.
const maxJSONBody = 1 << 20

// writeJSON answers with v as JSON, never cached.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("[ui] json: %v", err)
		http.Error(w, "the answer couldn't be made", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// apiError is what a refused request answers: what went wrong, to show.
type apiError struct {
	Error string `json:"error"`
}

// fail answers a refused request with message, to show as it is.
func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, apiError{Error: message})
}

// done answers a write that has nothing to say back.
func done(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, struct{}{})
}

// readJSON reads a request's JSON body into v. Only JSON is taken, which a
// page of another site can't send without the browser asking first.
func readJSON(r *http.Request, v any) error {
	if kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); kind != "application/json" {
		return errors.New("the request must be JSON")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return errors.New("the request couldn't be read")
	}
	return nil
}
