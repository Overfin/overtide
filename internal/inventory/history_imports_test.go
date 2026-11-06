package inventory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
)

// The import sync keeps each import for History with its title's name and
// what brought it; a title no longer known is named by its release. The
// first sync reads the whole history, whatever the cursor says.
func TestImportsKeptForHistory(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/history" || r.URL.Query().Get("includeEpisode") != "true" {
			w.Write([]byte(`{}`))
			return
		}
		w.Write([]byte(`{"totalRecords":2,"records":[
			{"id":11,"seriesId":4,"episodeId":40,"downloadId":"AA","eventType":"downloadFolderImported","date":"2026-11-06T10:00:00Z","sourceTitle":"Show.S02E03",
			 "data":{"importedPath":"/tv/Show/Season 2/e3.mkv","size":"1000"},"episode":{"seasonNumber":2,"episodeNumber":3}},
			{"id":10,"seriesId":5,"episodeId":50,"downloadId":"BB","eventType":"downloadFolderImported","date":"2020-01-01T10:00:00Z","sourceTitle":"Gone.S01E01",
			 "data":{"size":"7"},"episode":{"seasonNumber":1,"episodeNumber":1}}]}`))
	}))
	defer fake.Close()
	database, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.SetMeta("sonarr.s.history.cursor", "2026-11-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	service := New(config.Config{Services: []config.Service{{ID: "s", Type: "sonarr", Name: "Sonarr", URL: fake.URL, APIKey: "k"}}}, database)
	titles := importedTitles([]model.Media{{ServiceID: "s", Type: model.Series, SourceID: 4, Title: "Show", Year: 2001, Tags: []string{model.TagRequested}}})
	if _, _, _, err := service.syncImportHistory(context.Background(), titles); err != nil {
		t.Fatal(err)
	}
	if done, _ := database.Meta(historyBackfilledKey); done == "" {
		t.Fatal("History not marked as filled")
	}
	rows, total, err := database.Activities(store.ActivityQuery{})
	if err != nil || total != 2 {
		t.Fatalf("%d rows, %v", total, err)
	}
	show, gone := rows[0], rows[1]
	if show.Title != "Show" || show.Year != 2001 || show.Origin != "wish" || show.Bytes != 1000 || len(show.Episodes) != 1 || show.Episodes[0] != [2]int{2, 3} ||
		show.ServiceName != "Sonarr" || show.Day != time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC).In(time.Local).Format(time.DateOnly) {
		t.Fatalf("show: %+v", show)
	}
	if gone.Title != "Gone.S01E01" || gone.Origin != "" {
		t.Fatalf("a title no longer known: %+v", gone)
	}
	files, err := database.ImportedFilesOn("s", "series", 4, show.Day)
	if err != nil || len(files) != 1 || files[0].Path != "/tv/Show/Season 2/e3.mkv" || files[0].SourceTitle != "Show.S02E03" {
		t.Fatalf("files %+v, %v", files, err)
	}
}
