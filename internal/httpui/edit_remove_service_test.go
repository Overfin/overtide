package httpui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEditServiceEndToEndOverHTTP(t *testing.T) {
	oldRadarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer oldRadarr.Close()
	newRadarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer newRadarr.Close()
	handler, configPath := newServiceTestServer(t, nil)

	if response := post(t, handler, "/api/services", serviceForm{Type: "radarr", Name: "Movies", URL: oldRadarr.URL, APIKey: "key"}, nil); response.Code != http.StatusOK {
		t.Fatalf("setup: expected 200 adding Movies, got status=%d body=%q", response.Code, response.Body.String())
	}
	id := reloadConfig(t, configPath).Services[0].ID

	// The edit window's values load via GET, with the existing URL, and
	// never the saved key.
	got := getPath(t, handler, "/api/services/"+id, nil)
	detail := decode[serviceDetail](t, got)
	if got.Code != http.StatusOK || detail.URL != oldRadarr.URL || !detail.HasAPIKey {
		t.Fatalf("expected the edit window's values with the current URL, got status=%d body=%q", got.Code, got.Body.String())
	}
	if strings.Contains(got.Body.String(), `"key"`) {
		t.Fatalf("the saved API key was sent back: %q", got.Body.String())
	}

	// Moving it to a brand new URL keeps the same ID.
	if response := send(t, handler, http.MethodPut, "/api/services/"+id, serviceForm{Name: "Movies", URL: newRadarr.URL, APIKey: "newkey"}, nil); response.Code != http.StatusOK {
		t.Fatalf("expected 200 on a successful edit, got status=%d body=%q", response.Code, response.Body.String())
	}
	reloaded := reloadConfig(t, configPath)
	if len(reloaded.Services) != 1 || reloaded.Services[0].ID != id || reloaded.Services[0].URL != newRadarr.URL || reloaded.Services[0].APIKey != "newkey" {
		t.Fatalf("expected the same ID with the new URL persisted, got %#v (want id %q)", reloaded.Services, id)
	}

	// An edit with the key left empty keeps the saved one.
	if response := send(t, handler, http.MethodPut, "/api/services/"+id, serviceForm{Name: "Movies", URL: newRadarr.URL}, nil); response.Code != http.StatusOK {
		t.Fatalf("an edit without a key: status=%d body=%q", response.Code, response.Body.String())
	}
	if key := reloadConfig(t, configPath).Services[0].APIKey; key != "newkey" {
		t.Fatalf("an empty key field cleared the saved key: %q", key)
	}
}

// TestEditServiceRoundTripsAllowAutomaticRemoval guards a real gap: the edit
// window only shows one credential side and carries the other forward from
// the existing service, but nothing did the same for
// AllowAutomaticRemoval — the edit window always pre-fills the checkbox
// from the current value, and its submitted state is what persists, so an
// edit unrelated to automatic removal (renaming the service, changing its
// URL) must not silently reset it to false.
func TestEditServiceRoundTripsAllowAutomaticRemoval(t *testing.T) {
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer radarr.Close()
	handler, configPath := newServiceTestServer(t, nil)

	if response := post(t, handler, "/api/services", serviceForm{Type: "radarr", Name: "Movies", URL: radarr.URL, APIKey: "key", AllowAutomaticRemoval: true}, nil); response.Code != http.StatusOK {
		t.Fatalf("setup: expected 200 adding Movies, got status=%d body=%q", response.Code, response.Body.String())
	}
	id := reloadConfig(t, configPath).Services[0].ID

	// The edit window's GET must pre-fill the checkbox as checked.
	if detail := decode[serviceDetail](t, getPath(t, handler, "/api/services/"+id, nil)); !detail.AllowAutomaticRemoval {
		t.Fatalf("expected the edit window to show the checkbox checked, got %#v", detail)
	}

	// An edit that resubmits the checkbox as checked must keep it true, not
	// silently drop back to Go's zero value.
	if response := send(t, handler, http.MethodPut, "/api/services/"+id, serviceForm{Name: "Movies 4K", URL: radarr.URL, APIKey: "key", AllowAutomaticRemoval: true}, nil); response.Code != http.StatusOK {
		t.Fatalf("expected 200 on a successful edit, got status=%d body=%q", response.Code, response.Body.String())
	}
	if services := reloadConfig(t, configPath).Services; len(services) != 1 || !services[0].AllowAutomaticRemoval {
		t.Fatalf("expected AllowAutomaticRemoval to survive an unrelated edit, got %#v", services)
	}
}

func TestRemoveServiceEndToEndOverHTTP(t *testing.T) {
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer radarr.Close()
	handler, configPath := newServiceTestServer(t, nil)

	if response := post(t, handler, "/api/services", serviceForm{Type: "radarr", Name: "Movies", URL: radarr.URL, APIKey: "key"}, nil); response.Code != http.StatusOK {
		t.Fatalf("setup: expected 200 adding Movies, got status=%d body=%q", response.Code, response.Body.String())
	}
	id := reloadConfig(t, configPath).Services[0].ID

	if response := send(t, handler, http.MethodDelete, "/api/services/"+id, nil, nil); response.Code != http.StatusOK {
		t.Fatalf("expected 200 on a successful remove, got status=%d body=%q", response.Code, response.Body.String())
	}
	if services := reloadConfig(t, configPath).Services; len(services) != 0 {
		t.Fatalf("expected the service to be gone, got %#v", services)
	}
	if list := decode[servicesData](t, getPath(t, handler, "/api/services", nil)); len(list.Services) != 0 {
		t.Fatalf("expected the Services list to no longer show the removed service, got %#v", list)
	}
	if response := getPath(t, handler, "/api/services/"+id, nil); response.Code != http.StatusNotFound {
		t.Fatalf("a removed service's window: status=%d", response.Code)
	}
}

// A saved key is never sent to another server: moving a service to a new
// address with the key left empty is refused, and the new address hears
// nothing; the same server under another spelling (case, default port)
// still keeps the key.
func TestEditServiceNeverSendsTheSavedKeyToANewAddress(t *testing.T) {
	radarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer radarr.Close()
	var leaked atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "" || strings.Contains(r.URL.RawQuery, "secret-key") {
			leaked.Store(true)
		}
		w.Write([]byte(`{}`))
	}))
	defer elsewhere.Close()
	handler, configPath := newServiceTestServer(t, nil)
	if response := post(t, handler, "/api/services", serviceForm{Type: "radarr", Name: "Movies", URL: radarr.URL, APIKey: "secret-key"}, nil); response.Code != http.StatusOK {
		t.Fatalf("setup: %d %q", response.Code, response.Body.String())
	}
	id := reloadConfig(t, configPath).Services[0].ID

	response := send(t, handler, http.MethodPut, "/api/services/"+id, serviceForm{Name: "Movies", URL: elsewhere.URL}, nil)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "enter the API key again") {
		t.Fatalf("moving without the key: %d %q", response.Code, response.Body.String())
	}
	if leaked.Load() {
		t.Fatal("the saved key reached the new address")
	}
	if saved := reloadConfig(t, configPath).Services[0]; saved.URL != radarr.URL || saved.APIKey != "secret-key" {
		t.Fatalf("a refused move changed the service: %+v", saved)
	}

	// The same server, written differently (the scheme's case, a trailing
	// slash): the key stays. TestSameServer covers the other spellings.
	same := strings.ToUpper(radarr.URL[:4]) + radarr.URL[4:] + "/"
	if response := send(t, handler, http.MethodPut, "/api/services/"+id, serviceForm{Name: "Movies", URL: same}, nil); response.Code != http.StatusOK {
		t.Fatalf("the same server: %d %q", response.Code, response.Body.String())
	}
	if key := reloadConfig(t, configPath).Services[0].APIKey; key != "secret-key" {
		t.Fatalf("the key was dropped for the same server: %q", key)
	}
}

func TestSameServer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"http://radarr:7878", "http://RADARR:7878/", true},
		{"https://example.org", "https://example.org:443/radarr", true},
		{"http://example.org", "http://example.org:80", true},
		{"http://radarr:7878", "http://radarr:7879", false},
		{"http://radarr:7878", "https://radarr:7878", false},
		{"http://radarr:7878", "http://evil.example.org:7878", false},
		{"http://radarr:7878", "not a url", false},
		{"", "http://radarr:7878", false},
	} {
		if got := sameServer(c.a, c.b); got != c.want {
			t.Errorf("sameServer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
