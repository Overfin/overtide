package radarr

// ListMovie is a film on Radarr's enabled import lists, with the lists it's
// on, by ID.
type ListMovie struct {
	TMDBID int    `json:"tmdbId"`
	Title  string `json:"title"`
	Lists  []int  `json:"lists"`
}

// ListMovies returns the films Radarr's enabled import lists had at their
// last sync, whether or not the lists add them: Radarr keeps them all.
func (client *Client) ListMovies() ([]ListMovie, error) {
	var movies []ListMovie
	return movies, client.Patient().Get("/api/v3/importlist/movie", &movies)
}
