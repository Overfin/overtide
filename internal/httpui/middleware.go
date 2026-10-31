package httpui

import (
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// sameOriginWrites is a CSRF-style deployment-safety guard, layered
// underneath — not instead of — the session-based login enforced by
// authGate (see auth.go). A write must come from this site (its Origin, when
// sent) and, when it has a body (POST, PUT, PATCH), be JSON: a page of
// another site can't send JSON without the browser asking first, even when
// Origin is missing. DELETE can't be sent from another origin either, for
// the same reason.
func sameOriginWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					fail(w, http.StatusForbidden, "cross-origin write rejected")
					return
				}
			}
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			if kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); kind != "application/json" {
				fail(w, http.StatusUnsupportedMediaType, "the request must be JSON")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// contentSecurity lets the panel load only its own files: no inline script
// or style, nothing from elsewhere, and no other site's frame around it.
const contentSecurity = "default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders keeps Overtide's pages out of other sites' frames, where
// a page laid over them could have the admin click Remove unawares, allows
// only its own files, and stops browsers guessing a response's type.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurity)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
