package httpui

import (
	"testing"

	"github.com/overfin/overtide/internal/model"
)

func TestMediaTypeLabelAndAvailableMediaTypes(t *testing.T) {
	items := []model.Media{{Type: model.Movie}, {Type: model.Movie}, {Type: model.Series}, {Type: model.MediaType("artist")}}
	types := availableMediaTypes(items)
	if len(types) != 3 || types[0] != model.Movie || types[1] != model.Series || types[2] != model.MediaType("artist") {
		t.Fatalf("expected [movie series artist] in that order, got %#v", types)
	}
	if mediaTypeLabel(model.Movie) != "Movies" || mediaTypeLabel(model.Series) != "Series" {
		t.Fatalf("expected established labels for movie/series")
	}
	if got := mediaTypeLabel(model.MediaType("artist")); got != "Artists" {
		t.Fatalf("expected a future type to get a naive title-case-plus-s label, got %q", got)
	}
}

func TestNormalizeMediaTypeFilterRejectsUnavailableType(t *testing.T) {
	available := []model.MediaType{model.Movie}
	if got := normalizeMediaTypeFilter("series", available); got != "any" {
		t.Fatalf("expected a type absent from the library to normalize to any, got %q", got)
	}
	if got := normalizeMediaTypeFilter("movie", available); got != "movie" {
		t.Fatalf("expected an available type to pass through, got %q", got)
	}
}

func TestMediaSourceForSuppressesRootLabelWithSingleRoot(t *testing.T) {
	roots := map[string][]string{"Movies": {"/data/movies"}}
	media := model.Media{ServiceName: "Movies", Path: "/data/movies/Film (2024)/film.mkv"}
	source := mediaSourceFor(media, roots)
	if source.Label != "Movies" {
		t.Fatalf("expected no root suffix for a single-root service, got %q", source.Label)
	}
}

func TestMediaSourceForAddsRootLabelWithMultipleRoots(t *testing.T) {
	roots := map[string][]string{"Movies": {"/data/movies", "/data/movies-4k"}}
	media := model.Media{ServiceName: "Movies", Path: "/data/movies-4k/Film (2024)/film.mkv"}
	source := mediaSourceFor(media, roots)
	if source.Label != "Movies · movies-4k" {
		t.Fatalf("expected a root-qualified label for a multi-root service, got %q", source.Label)
	}
	other := mediaSourceFor(model.Media{ServiceName: "Movies", Path: "/data/movies/Other (2020)/other.mkv"}, roots)
	if other.Key == source.Key {
		t.Fatalf("expected distinct sources for distinct roots of the same service")
	}
}
