package adding

import (
	"context"
	"errors"
	"testing"

	"github.com/overfin/overtide/internal/arrfake"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
)

// A title removed by hand leaves the list at once and for good, whoever
// saves the list after; one downloading isn't removed.
func TestRemoveTitle(t *testing.T) {
	now := *arrfake.Day("2026-11-10")
	service := newTestService(t, config.Config{}, now)
	entries := []Entry{
		{Key: "series:1", Kind: model.Series, ServiceID: "s", Title: "Doraemon", Source: FromOverfin, State: Waiting, AddedAt: now},
		{Key: "movie:2", Kind: model.Movie, ServiceID: "r", Title: "Heat", Source: FromOverfin, State: Downloading, AddedAt: now},
	}
	if err := service.save(entries); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := service.RemoveTitle(ctx, "s|series:1"); err != nil {
		t.Fatal(err)
	}
	if err := service.RemoveTitle(ctx, "r|movie:2"); !errors.Is(err, ErrDownloading) {
		t.Fatalf("downloading: %v", err)
	}
	if err := service.RemoveTitle(ctx, "s|series:9"); !errors.Is(err, ErrNotInList) {
		t.Fatalf("not there: %v", err)
	}
	// A run that read the list before saves it whole: it stays out.
	if err := service.save(entries); err != nil {
		t.Fatal(err)
	}
	got, err := service.Entries()
	if err != nil || len(got) != 1 || got[0].Key != "movie:2" {
		t.Fatalf("after: %+v %v", got, err)
	}
}

// Overfin's suggestions in the import-list exclusions leave the list;
// wishes and moving titles stay.
func TestDropExcluded(t *testing.T) {
	now := *arrfake.Day("2026-11-10")
	service := newTestService(t, config.Config{}, now)
	entries := []Entry{
		{Key: "series:1", ServiceID: "s", Source: FromOverfin, State: Waiting},
		{Key: "series:2", ServiceID: "s", Source: FromRequest, State: Waiting},
		{Key: "series:3", ServiceID: "s", Source: FromOverfin, State: Downloading},
		{Key: "series:4", ServiceID: "s", Source: FromOverfin, State: Waiting},
	}
	kept := service.dropUnwanted(entries, map[string]bool{"s|series:1": true, "s|series:2": true, "s|series:3": true})
	var keys []string
	for _, e := range kept {
		keys = append(keys, e.Key)
	}
	if len(keys) != 3 || keys[0] != "series:2" || keys[1] != "series:3" || keys[2] != "series:4" {
		t.Fatalf("kept %v", keys)
	}
}
