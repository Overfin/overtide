package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// AddingConfig is how Overtide brings titles in.
type AddingConfig struct {
	// Mode is AddingOff (also what empty means), AddingDryRun or AddingOn.
	Mode string `json:"mode"`
	// Manage is ManageOwn (also what empty means) or ManageAll.
	Manage string `json:"manage,omitempty"`
	// Searches is how many searches a run (every 15 minutes) may ask
	// for; 0 means DefaultSearches.
	Searches int `json:"searches,omitempty"`
	// TitlesPerDay is how many titles may start downloading in 24 hours
	// (a series, one per season), whatever brought them; 0 means
	// DefaultTitlesPerDay.
	TitlesPerDay int `json:"titles_per_day,omitempty"`
	// UpgradesPerDay is how many searches for a better copy may go in 24
	// hours; 0 means none.
	UpgradesPerDay int `json:"upgrades_per_day,omitempty"`
	// Destinations say where titles go, in order (see DestinationFor).
	Destinations []Destination `json:"destinations,omitempty"`
	// EventsURL is Overtide's address as Radarr and Sonarr reach it, for
	// the events they send (a title added) with "Manage all": found and
	// tested by Overtide when it can, else set by the operator.
	EventsURL string `json:"events_url,omitempty"`
}

// Destination is where titles go: a Radarr (films) or a Sonarr (series),
// its root folder and quality profile; for one of Overfin's categories
// (its ID), or for every title of that kind (Category "").
type Destination struct {
	Category         string `json:"category,omitempty"`
	ServiceID        string `json:"service_id"`
	RootFolder       string `json:"root_folder"`
	QualityProfileID int    `json:"quality_profile_id"`
}

// DestinationFor is where a title of a kind ("radarr" for a film,
// "sonarr" for a series) goes, given the categories it fits: the first
// destination of a category it fits, else the first of every title of
// that kind. False when none is set: the first Radarr or Sonarr then,
// with its only root folder and quality profile.
func (configuration Config) DestinationFor(serviceType string, categories []string) (Destination, bool) {
	ofKind := func(d Destination) bool {
		svc, ok := configuration.service(d.ServiceID)
		return ok && svc.Type == serviceType
	}
	for _, d := range configuration.Adding.Destinations {
		if d.Category != "" && ofKind(d) && slices.Contains(categories, d.Category) {
			return d, true
		}
	}
	for _, d := range configuration.Adding.Destinations {
		if d.Category == "" && ofKind(d) {
			return d, true
		}
	}
	return Destination{}, false
}

func (configuration Config) service(id string) (Service, bool) {
	for _, svc := range configuration.Services {
		if svc.ID == id {
			return svc, true
		}
	}
	return Service{}, false
}

// SetDestination adds a destination (index -1) or replaces one; the
// caller checked its folder and profile with the service.
func SetDestination(configuration Config, index int, d Destination) (Config, error) {
	svc, ok := configuration.service(d.ServiceID)
	switch {
	case !ok || (svc.Type != "radarr" && svc.Type != "sonarr"):
		return configuration, fmt.Errorf("a destination is a Radarr or a Sonarr")
	case d.RootFolder == "" || d.QualityProfileID <= 0:
		return configuration, fmt.Errorf("a destination needs a root folder and a quality profile")
	case index >= len(configuration.Adding.Destinations):
		return configuration, fmt.Errorf("destination not found")
	}
	updated := configuration
	updated.Adding.Destinations = append([]Destination(nil), configuration.Adding.Destinations...)
	if index < 0 {
		updated.Adding.Destinations = append(updated.Adding.Destinations, d)
	} else {
		updated.Adding.Destinations[index] = d
	}
	return updated, nil
}

// RemoveDestination takes a destination out.
func RemoveDestination(configuration Config, index int) (Config, error) {
	if index < 0 || index >= len(configuration.Adding.Destinations) {
		return configuration, fmt.Errorf("destination not found")
	}
	updated := configuration
	updated.Adding.Destinations = slices.Delete(slices.Clone(configuration.Adding.Destinations), index, index+1)
	return updated, nil
}

// DefaultSearches and MaxSearches bound AddingConfig.Searches: a cautious
// pace by default, for the trackers' limits.
const (
	DefaultSearches = 3
	MaxSearches     = 60
)

// DefaultTitlesPerDay and MaxTitlesPerDay bound AddingConfig.TitlesPerDay,
// and MaxUpgradesPerDay AddingConfig.UpgradesPerDay.
const (
	DefaultTitlesPerDay = 10
	MaxTitlesPerDay     = 500
	MaxUpgradesPerDay   = 500
)

// PerDay is how many titles may start downloading in 24 hours.
func (adding AddingConfig) PerDay() int {
	if adding.TitlesPerDay <= 0 {
		return DefaultTitlesPerDay
	}
	return min(adding.TitlesPerDay, MaxTitlesPerDay)
}

// SetUpgradesPerDay sets how many searches for a better copy may go in 24
// hours; 0 means none.
func SetUpgradesPerDay(configuration Config, upgrades int) (Config, error) {
	if upgrades < 0 || upgrades > MaxUpgradesPerDay {
		return configuration, fmt.Errorf("upgrades a day must be between 0 and %d", MaxUpgradesPerDay)
	}
	updated := configuration
	updated.Adding.UpgradesPerDay = upgrades
	return updated, nil
}

// SetEventsURL sets Overtide's address as Radarr and Sonarr reach it: an
// http or https address, or "" for Overtide to find it.
func SetEventsURL(configuration Config, address string) (Config, error) {
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	if address != "" {
		u, err := url.Parse(address)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return configuration, fmt.Errorf("Overtide's address for Radarr and Sonarr must be an http or https address, like http://overtide:8088")
		}
	}
	updated := configuration
	updated.Adding.EventsURL = address
	return updated, nil
}

// SetTitlesPerDay sets how many titles may start downloading in 24 hours.
func SetTitlesPerDay(configuration Config, titles int) (Config, error) {
	if titles < 1 || titles > MaxTitlesPerDay {
		return configuration, fmt.Errorf("titles a day must be between 1 and %d", MaxTitlesPerDay)
	}
	updated := configuration
	updated.Adding.TitlesPerDay = titles
	return updated, nil
}

// SearchesPerRun is how many searches a run may ask for.
func (adding AddingConfig) SearchesPerRun() int {
	if adding.Searches <= 0 {
		return DefaultSearches
	}
	return min(adding.Searches, MaxSearches)
}

// SetAddingSearches sets how many searches a run may ask for.
func SetAddingSearches(configuration Config, searches int) (Config, error) {
	if searches < 1 || searches > MaxSearches {
		return configuration, fmt.Errorf("searches every 15 minutes must be between 1 and %d", MaxSearches)
	}
	updated := configuration
	updated.Adding.Searches = searches
	return updated, nil
}

const (
	AddingOff    = "off"
	AddingDryRun = "dry_run"
	AddingOn     = "on"
)

// What Overtide manages in Radarr and Sonarr: the titles it adds, or
// every title.
const (
	ManageOwn = "own"
	ManageAll = "all"
)

// ManagesAll reports whether Overtide manages every title in Radarr and
// Sonarr.
func (adding AddingConfig) ManagesAll() bool { return adding.Manage == ManageAll }

// SetAddingManage sets what Overtide manages.
func SetAddingManage(configuration Config, manage string) (Config, error) {
	switch manage {
	case ManageOwn, ManageAll:
	default:
		return configuration, fmt.Errorf("what Overtide manages must be %q or %q", ManageOwn, ManageAll)
	}
	updated := configuration
	updated.Adding.Manage = manage
	return updated, nil
}

// SetAddingMode sets how titles are brought in.
func SetAddingMode(configuration Config, mode string) (Config, error) {
	switch mode {
	case AddingOff, AddingDryRun, AddingOn:
	default:
		return configuration, fmt.Errorf("adding mode must be one of %q, %q, %q", AddingOff, AddingDryRun, AddingOn)
	}
	updated := configuration
	updated.Adding.Mode = mode
	return updated, nil
}
