package config

import "testing"

// A title goes to the first destination of a category it fits, else to
// its kind's default; none set, there's no destination. Removing a
// service takes its destinations out.
func TestDestinationFor(t *testing.T) {
	cfg := Config{Services: []Service{{ID: "r", Type: "radarr"}, {ID: "r2", Type: "radarr"}, {ID: "s", Type: "sonarr"}}}
	if _, ok := cfg.DestinationFor("radarr", nil); ok {
		t.Fatal("a destination with none set")
	}
	var err error
	for _, d := range []Destination{
		{ServiceID: "r", RootFolder: "/films", QualityProfileID: 1},
		{Category: "anime", ServiceID: "r2", RootFolder: "/anime", QualityProfileID: 2},
		{Category: "anime", ServiceID: "s", RootFolder: "/anime-series", QualityProfileID: 3},
		{Category: "kids", ServiceID: "r", RootFolder: "/kids", QualityProfileID: 1},
	} {
		if cfg, err = SetDestination(cfg, -1, d); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		kind       string
		categories []string
		want       string
		ok         bool
	}{
		{"radarr", nil, "/films", true},
		{"radarr", []string{"films", "anime"}, "/anime", true},
		{"radarr", []string{"kids", "anime"}, "/anime", true}, // the first set wins
		{"sonarr", []string{"anime"}, "/anime-series", true},
		{"sonarr", []string{"kids"}, "", false}, // no series default
	} {
		d, ok := cfg.DestinationFor(c.kind, c.categories)
		if ok != c.ok || d.RootFolder != c.want {
			t.Errorf("%s %v: %+v %v", c.kind, c.categories, d, ok)
		}
	}
	if _, err := SetDestination(cfg, -1, Destination{ServiceID: "nope", RootFolder: "/x", QualityProfileID: 1}); err == nil {
		t.Error("a destination on no service")
	}
	if cfg, err = RemoveService(cfg, "r2"); err != nil || len(cfg.Adding.Destinations) != 3 {
		t.Fatalf("after removing r2: %+v %v", cfg.Adding.Destinations, err)
	}
	if cfg, err = RemoveDestination(cfg, 0); err != nil || cfg.Adding.Destinations[0].Category != "anime" {
		t.Fatalf("after removing the first: %+v %v", cfg.Adding.Destinations, err)
	}
}
