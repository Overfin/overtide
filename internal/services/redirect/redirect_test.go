package redirect

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	var got []string
	end := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.Host+r.URL.Path+" key="+r.Header.Get("X-Api-Key"))
	}))
	defer end.Close()
	port := end.URL[strings.LastIndex(end.URL, ":"):]
	sameHost := "http://127.0.0.1" + port
	otherHost := "http://localhost" + port

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
		got = nil
		start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, tc.to+"/next", tc.status)
		}))
		req, _ := http.NewRequest(tc.method, start.URL+"/api", nil)
		req.Header.Set("X-Api-Key", "secret")
		res, err := (&http.Client{CheckRedirect: Check}).Do(req)
		start.Close()
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
