package httpui

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/overfin/overtide/internal/config"
)

// Overfin's measures, on the Statistics page: not connected without
// Overfin, the numbers with it, and why not when it refuses the key.
func TestStatisticsMeasures(t *testing.T) {
	_, handler := libraryServer(t, config.Config{})
	if got, code := libraryGet[measuresData](t, handler, "/api/statistics/measures"); code != 200 || got.Connected || got.Measures != nil {
		t.Fatalf("without Overfin: %d %+v", code, got)
	}

	allowed := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apps/v1/measures" || r.Header.Get("Authorization") != "Bearer ofapp_k" {
			http.NotFound(w, r)
			return
		}
		if !allowed {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"type":"not-authenticated"}`))
			return
		}
		w.Write([]byte(`{"days":90,"arrived":10,"started":4,"finished":2,"left":5,"missed":1,"library":100,"watched":20}`))
	}))
	defer srv.Close()
	cfg := config.Config{}
	cfg.Overfin = config.Connection{URL: srv.URL, APIKey: "ofapp_k"}
	_, handler = libraryServer(t, cfg)
	got, code := libraryGet[measuresData](t, handler, "/api/statistics/measures")
	if code != 200 || !got.Connected || got.Measures == nil || got.Measures.Started != 4 || got.Measures.Watched != 20 {
		t.Fatalf("with Overfin: %d %+v", code, got)
	}
	allowed = false
	if got, _ := libraryGet[measuresData](t, handler, "/api/statistics/measures"); !got.Connected || got.Measures != nil || got.Problem == "" {
		t.Errorf("refused: %+v", got)
	}
}
