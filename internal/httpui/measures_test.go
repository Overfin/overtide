package httpui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/store"
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

// Disk use on the Statistics page names each disk as the Storage page
// does; one Overtide no longer knows keeps its path.
func TestStatisticsDiskNames(t *testing.T) {
	cfg := config.Config{}
	cfg.Storage.DeviceThresholds = map[string]config.DeviceThreshold{"/data/Films": {Name: "Media"}}
	server, handler := libraryServer(t, cfg)
	day := time.Now().UTC().Format(time.DateOnly)
	for _, device := range []string{"/data/Films", "/old"} {
		if err := server.inv.Store().KeepDiskSample(store.DiskSample{Day: day, Device: device, UsedBytes: 1, TotalBytes: 2}); err != nil {
			t.Fatal(err)
		}
	}
	got, code := libraryGet[statsData](t, handler, "/api/statistics?range=30d")
	names := map[string]string{}
	for _, d := range got.Disks {
		names[d.Device] = d.Name
	}
	if code != 200 || names["/data/Films"] != "Media" || names["/old"] != "" || len(names) != 2 {
		t.Errorf("disks: %d %+v", code, got.Disks)
	}
}
