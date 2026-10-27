package httpui

import "testing"

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
	}
	for back, want := range cases {
		if got := localPath(back, "/storage"); got != want {
			t.Errorf("localPath(%q) = %q, want %q", back, got, want)
		}
	}
}
