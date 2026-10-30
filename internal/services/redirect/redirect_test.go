package redirect

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	var got []string
	var to string
	var status int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			http.Redirect(w, r, to+"/next", status)
			return
		}
		got = append(got, r.Method+" "+r.Host+r.URL.Path+" key="+r.Header.Get("X-Api-Key"))
	}))
	defer srv.Close()
	sameHost := srv.URL
	otherHost := "http://localhost" + srv.URL[strings.LastIndex(srv.URL, ":"):]

	for _, tc := range []struct {
		name, method, to string
		status           int
		reaches          bool
	}{
		{"a GET on the same host", http.MethodGet, sameHost, http.StatusFound, true},
		{"a DELETE kept by a 307", http.MethodDelete, sameHost, http.StatusTemporaryRedirect, true},
		{"a DELETE a 302 would make a GET", http.MethodDelete, sameHost, http.StatusFound, false},
		{"a POST a 301 would make a GET", http.MethodPost, sameHost, http.StatusMovedPermanently, false},
		{"a GET to another host", http.MethodGet, otherHost, http.StatusFound, false},
		{"a DELETE to another host, even by a 307", http.MethodDelete, otherHost, http.StatusTemporaryRedirect, false},
	} {
		got, to, status = nil, tc.to, tc.status
		req, _ := http.NewRequest(tc.method, srv.URL+"/api", nil)
		req.Header.Set("X-Api-Key", "secret")
		res, err := (&http.Client{CheckRedirect: Check}).Do(req)
		if res != nil {
			res.Body.Close()
		}
		if tc.reaches && (err != nil || len(got) != 1 || !strings.HasPrefix(got[0], tc.method+" ")) {
			t.Errorf("%s: err %v, reached %v", tc.name, err, got)
		}
		if !tc.reaches && (err == nil || len(got) != 0 || !strings.Contains(err.Error(), "not followed")) {
			t.Errorf("%s: err %v, reached %v", tc.name, err, got)
		}
	}
}

func TestCheckRefusesHTTPSToHTTP(t *testing.T) {
	first, _ := http.NewRequest(http.MethodGet, "https://radarr.example/api", nil)
	next, _ := http.NewRequest(http.MethodGet, "http://radarr.example/api", nil)
	if err := Check(next, []*http.Request{first}); err == nil {
		t.Fatal("https to http followed")
	}
	up, _ := http.NewRequest(http.MethodGet, "https://radarr.example/api", nil)
	plain, _ := http.NewRequest(http.MethodGet, "http://radarr.example/api", nil)
	if err := Check(up, []*http.Request{plain}); err != nil {
		t.Fatalf("http to https refused: %v", err)
	}
}

func TestCheckStaysOnTheService(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		follow   bool
	}{
		{"http://radarr.example:7878/api", "http://radarr.example:7878/api/", true},
		{"http://radarr.example:7878/api", "http://RADARR.example:7878/api/", true},
		{"http://radarr.example/api", "https://radarr.example/api", true},
		{"http://radarr.example:80/api", "https://radarr.example:443/api", true},
		{"http://radarr.example:7878/api", "http://radarr.example:8080/api", false},
		{"http://radarr.example:7878/api", "https://radarr.example/api", false},
		{"http://radarr.example/api", "https://radarr.example:8443/api", false},
		{"https://radarr.example/api", "https://radarr.example:8443/api", false},
	} {
		first, _ := http.NewRequest(http.MethodGet, tc.from, nil)
		next, _ := http.NewRequest(http.MethodGet, tc.to, nil)
		if err := Check(next, []*http.Request{first}); (err == nil) != tc.follow {
			t.Errorf("%s → %s: %v, want followed %v", tc.from, tc.to, err, tc.follow)
		}
	}
}
