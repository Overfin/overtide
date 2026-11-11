package httpui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/store"
)

func TestClientLoginKeyTrustedProxies(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32"), netip.MustParsePrefix("198.51.100.0/24")}
	cases := []struct {
		name      string
		trusted   []netip.Prefix
		remote    string
		forwarded []string
		want      string
	}{
		{"no setting: the header is ignored", nil, "192.0.2.10:5000", []string{"203.0.113.7"}, "192.0.2.10"},
		{"no header from a trusted proxy", proxies, "192.0.2.10:5000", nil, "192.0.2.10"},
		{"trusted proxy: the client it heard from", proxies, "192.0.2.10:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"two trusted proxies in a row", proxies, "192.0.2.10:5000", []string{"203.0.113.7, 198.51.100.4"}, "203.0.113.7"},
		{"spoofed leftmost entries are ignored", proxies, "192.0.2.10:5000", []string{"203.0.113.99, 192.0.2.10, 203.0.113.7"}, "203.0.113.7"},
		{"spoofed entries in an earlier header are ignored", proxies, "192.0.2.10:5000", []string{"203.0.113.99", "203.0.113.7"}, "203.0.113.7"},
		{"untrusted connection: the header is ignored", proxies, "203.0.113.50:5000", []string{"203.0.113.7"}, "203.0.113.50"},
		{"garbage right of the client: the last trusted hop", proxies, "192.0.2.10:5000", []string{"203.0.113.7, nonsense"}, "192.0.2.10"},
		{"every entry a trusted proxy: the leftmost", proxies, "192.0.2.10:5000", []string{"198.51.100.4, 198.51.100.5"}, "198.51.100.4"},
		{"IPv6 client", proxies, "192.0.2.10:5000", []string{"2001:db8::1"}, "2001:db8::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/login", nil)
			r.RemoteAddr = c.remote
			for _, value := range c.forwarded {
				r.Header.Add("X-Forwarded-For", value)
			}
			if got := clientLoginKey(r, c.trusted); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// Behind a trusted proxy, a guesser locked out doesn't lock out the admin
// coming through the same proxy; without the setting, they share one count.
func TestLoginLimitBehindTrustedProxy(t *testing.T) {
	for _, c := range []struct {
		name        string
		config      string
		adminLocked bool
	}{
		{"trusted proxy", `{"server":{"trusted_proxies":["192.0.2.0/24"]},"storage":{}}`, false},
		{"no setting", `{"storage":{}}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			handler := newProxyTestServer(t, c.config)
			setUp(t, handler)
			for range 6 {
				proxiedLogin(t, handler, "203.0.113.66", "wrong-password")
			}
			if code := proxiedLogin(t, handler, "203.0.113.66", "wrong-password"); code != http.StatusTooManyRequests {
				t.Fatalf("guesser: status %d, want locked out", code)
			}
			code := proxiedLogin(t, handler, "203.0.113.7", "correct-horse-battery")
			if locked := code == http.StatusTooManyRequests; locked != c.adminLocked {
				t.Fatalf("admin: status %d, locked out %v, want %v", code, locked, c.adminLocked)
			}
		})
	}
}

func newProxyTestServer(t *testing.T, body string) http.Handler {
	t.Helper()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := inventory.New(cfg, database)
	inv.SetConfigPath(configPath)
	server, err := New(inv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler()
}

// proxiedLogin signs in as client, through the proxy at 192.0.2.1
// (httptest's address), and returns the status.
func proxiedLogin(t *testing.T, handler http.Handler, client, password string) int {
	t.Helper()
	raw, _ := json.Marshal(credentials{Username: "admin", Password: password})
	request := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(raw))
	request.RemoteAddr = "192.0.2.1:40000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", client)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}
