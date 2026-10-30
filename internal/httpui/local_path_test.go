package httpui

import (
	"net/url"
	"strings"
	"testing"
)

func TestLocalPathKeepsOnlyPathsOnThisSite(t *testing.T) {
	cases := map[string]string{
		"/library?page=2":  "/library?page=2",
		"/":                "/",
		"":                 "/storage",
		"library":          "/storage",
		"https://evil.org": "/storage",
		"//evil.org":       "/storage",
		"/\\evil.org":      "/storage",
		"/\\/evil.org":     "/storage",
		"/library\\x":      "/storage",
		"/\t/evil.org":     "/storage",
		"/\n/evil.org":     "/storage",
		"/\r/evil.org":     "/storage",
		"/ /evil.org":      "/storage",
		"/library\x00":     "/storage",
	}
	for back, want := range cases {
		if got := localPath(back, "/storage"); got != want {
			t.Errorf("localPath(%q) = %q, want %q", back, got, want)
		}
	}
}

// FuzzLocalPath: whatever back is, the address is the fallback or a path on
// this site, as a browser reads it (it drops tabs and line breaks, and
// takes a backslash for a slash).
func FuzzLocalPath(f *testing.F) {
	for _, s := range []string{"/library", "//evil.org", "/\\evil.org", "/\t/evil.org", "/%2F/evil.org", "/library?x=//y", "/　/evil.org"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, back string) {
		got := localPath(back, "/storage")
		if got == "/storage" {
			return
		}
		read := strings.NewReplacer("\t", "", "\n", "", "\r", "", "\\", "/").Replace(got)
		// An address Go can't parse ("/%0") is still a path to a browser.
		u, err := url.Parse(read)
		if !strings.HasPrefix(read, "/") || strings.HasPrefix(read, "//") || (err == nil && (u.Host != "" || u.Scheme != "")) {
			t.Errorf("localPath(%q) = %q, which a browser reads as %q", back, got, read)
		}
	})
}
