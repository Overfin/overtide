package inventory

// Overtide's own changes to the files are kept apart. A removal makes its
// changes alone (LockFiles). Adding's steps that move or delete files
// share them (ShareFiles), each held for that step only: one download's
// import, one season imported again, one stalled download given up. So a
// removal waits for the step under way, never for adding's whole pass,
// and no step starts while a removal runs.

// ShareFiles holds the files for one of adding's steps; release lets them
// go. A removal waiting goes before the next step.
func (service *Service) ShareFiles() (release func()) {
	if service == nil {
		return func() {}
	}
	service.changes.RLock()
	return service.changes.RUnlock
}

// LockFiles holds the files for a removal alone: it waits for adding's
// step under way to end; release lets them go.
func (service *Service) LockFiles() (release func()) {
	if service == nil {
		return func() {}
	}
	service.changes.Lock()
	return service.changes.Unlock
}
