package overfin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overfin/overtide/internal/model"
)

// Overfin's scores, read page by page with the key: a film by its TMDB
// ID, a series' seasons by TMDB ID, else TVDB ID; what Overfin didn't
// score has none.
func TestApply(t *testing.T) {
	pages := map[string]string{
		"":  `{"titles":[{"kind":"movie","tmdbId":"10","score":61.5},{"kind":"season","tmdbId":"20","tvdbId":"200","season":1,"score":40}],"next":"2"}`,
		"2": `{"titles":[{"kind":"season","tmdbId":"20","tvdbId":"200","season":2,"score":75},{"kind":"season","tvdbId":"300","season":1,"score":12.5}]}`,
	}
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ofapp_k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := r.URL.Query()
		asked = append(asked, r.URL.Path+"?"+q.Get("inLibrary")+","+q.Get("fields")+","+q.Get("cursor"))
		w.Write([]byte(pages[q.Get("cursor")]))
	}))
	defer srv.Close()

	items := []model.Media{
		{Type: model.Movie, TMDBID: 10},
		{Type: model.Movie, TMDBID: 11},
		{Type: model.Series, TMDBID: 20, TVDBID: 200},
		{Type: model.Series, TVDBID: 300},
		{Type: model.Series, TMDBID: 99},
	}
	if err := New(srv.URL, "ofapp_k").Apply(items); err != nil {
		t.Fatal(err)
	}
	if strings.Join(asked, " ") != "/apps/v1/titles?true,score, /apps/v1/titles?true,score,2" {
		t.Errorf("asked: %v", asked)
	}
	if items[0].OverfinScore == nil || *items[0].OverfinScore != 61.5 || items[1].OverfinScore != nil {
		t.Errorf("films: %v %v", items[0].OverfinScore, items[1].OverfinScore)
	}
	if s := items[2].OverfinSeasonScores; len(s) != 2 || s[1] != 40 || s[2] != 75 {
		t.Errorf("by TMDB ID: %v", s)
	}
	if s := items[3].OverfinSeasonScores; len(s) != 1 || s[1] != 12.5 {
		t.Errorf("by TVDB ID: %v", s)
	}
	if items[4].OverfinSeasonScores != nil {
		t.Errorf("unknown: %v", items[4].OverfinSeasonScores)
	}
}

// A key Overfin refuses, or one without Values, says what to do.
func TestValidate(t *testing.T) {
	for status, want := range map[int]string{http.StatusUnauthorized: "refused the key", http.StatusForbidden: "Values"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			json.NewEncoder(w).Encode(map[string]string{"type": "x"})
		}))
		err := New(srv.URL, "ofapp_k").Validate()
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v", status, err)
		}
	}
}
