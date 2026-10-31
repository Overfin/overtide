package httpui

import (
	"testing"
)

// Every answer, signed in or not, refuses to be framed by another site and
// lets the page load only Overtide's own files.
func TestPagesRefuseFraming(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	for _, path := range []string{"/", "/library", "/api/session", "/api/dashboard"} {
		h := getPath(t, handler, path, nil).Header()
		if h.Get("Content-Security-Policy") != contentSecurity || h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %v", path, h)
		}
	}
}
