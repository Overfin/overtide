// Package redirect is the redirect rule for Overtide's clients of other
// services. Go's own rule hands their API key header to any host a
// redirect names, and turns a redirected DELETE or POST into a GET that
// can answer 200 though nothing was done.
package redirect

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Check follows a redirect only to the same host and port (or from http
// to https on their usual ports), never from https to http, and only when
// the request stays what it was (a GET, or a 307 or 308). Anything else is
// an error naming where the service sent it, for the operator to fix the
// service's address.
func Check(req *http.Request, via []*http.Request) error {
	first := via[0]
	switch {
	case len(via) >= 10:
		return fmt.Errorf("stopped after 10 redirects")
	case !sameService(first.URL, req.URL):
		return fmt.Errorf("redirected to another host or port (%s): not followed", req.URL.Redacted())
	case first.URL.Scheme == "https" && req.URL.Scheme != "https":
		return fmt.Errorf("redirected from https to %s: not followed", req.URL.Redacted())
	case req.Method != first.Method:
		return fmt.Errorf("redirected to %s, which would turn this %s into a %s: not followed", req.URL.Redacted(), first.Method, req.Method)
	}
	return nil
}

// sameService reports whether next is where from's service answers: the
// same host and port, or the same host moving from http to https, each on
// its usual port.
func sameService(from, next *url.URL) bool {
	if !strings.EqualFold(from.Hostname(), next.Hostname()) {
		return false
	}
	if from.Scheme == next.Scheme {
		return portOf(from) == portOf(next)
	}
	return from.Scheme == "http" && next.Scheme == "https" && portOf(from) == "80" && portOf(next) == "443"
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}
