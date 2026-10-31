package httpui

import (
	"encoding/json"

	"github.com/overfin/overtide/internal/removal"
	"github.com/overfin/overtide/internal/store"
)

type historyEventView struct {
	store.HistoryEvent
	Files   []removal.FileState
	Results []string
	Errors  []string
}

func historyEventViews(events []store.HistoryEvent) []historyEventView {
	views := make([]historyEventView, 0, len(events))
	for _, event := range events {
		view := historyEventView{HistoryEvent: event}
		var payload struct {
			Plan struct {
				Files []removal.FileState `json:"files"`
			} `json:"plan"`
			Results []string `json:"results"`
			Errors  []string `json:"errors"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			view.Files = payload.Plan.Files
			view.Results = payload.Results
			view.Errors = payload.Errors
		}
		views = append(views, view)
	}
	return views
}
