package httpui

import (
	"testing"
)

// Every page, signed in or not, refuses to be framed by another site.
func TestPagesRefuseFraming(t *testing.T) {
	handler, _ := newAuthTestServer(t)
	for _, path := range []string{"/setup", "/login", "/", "/assets/app.css"} {
		h := getPath(t, handler, path, nil).Header()
		if h.Get("Content-Security-Policy") != "frame-ancestors 'none'" || h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %v", path, h)
		}
	}
}
