package httpui

import (
	"github.com/overfin/overtide/internal/tasks"
)

type taskGroup struct {
	Name  string
	Tasks []tasks.Status
}

// taskGroups separates the flat task list into the "Library & Storage" core
// tasks (always shown) and one group per enrichment service, shown only
// when that service is actually configured.
func (server *Server) taskGroups(all []tasks.Status) []taskGroup {
	cfg := server.inv.Config()
	byID := make(map[string]tasks.Status, len(all))
	for _, status := range all {
		byID[status.ID] = status
	}
	var groups []taskGroup
	var core []tasks.Status
	for _, id := range []string{"inventory", "files", removalTaskID, autoRemovalTaskID} {
		if status, ok := byID[id]; ok {
			core = append(core, status)
		}
	}
	if len(core) > 0 {
		groups = append(groups, taskGroup{Name: "Library & Storage", Tasks: core})
	}
	if cfg.Overfin.URL != "" {
		var overfin []tasks.Status
		for _, id := range []string{"overfin"} {
			if status, ok := byID[id]; ok {
				overfin = append(overfin, status)
			}
		}
		if len(overfin) > 0 {
			groups = append(groups, taskGroup{Name: "Overfin", Tasks: overfin})
		}
	}
	return groups
}
