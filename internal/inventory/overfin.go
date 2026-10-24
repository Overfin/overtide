package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/valuation"
)

// RefreshOverfin reads Overfin's scores for what the libraries have. Like
// RefreshSeerr, it keeps its results if the base generation moves on
// mid-pass. While it runs, or after it failed, automatic removal planning
// is paused: with Overfin configured, its scores are the Retention Value.
func (service *Service) RefreshOverfin(ctx context.Context) error {
	defer service.publishChange()
	if service.cfg.Overfin.URL == "" {
		return nil
	}
	service.mu.Lock()
	if !service.reliability.Inventory || service.generation == 0 {
		service.mu.Unlock()
		return fmt.Errorf("base inventory is not available")
	}
	base := cloneMedia(service.items)
	client := service.overfin
	service.reliability.Overfin = "pending"
	service.reliability.Valuation = false
	service.reliability.Message = "Overfin scores are being read; automatic removal planning is paused."
	service.mu.Unlock()
	if err := client.WithContext(ctx).Apply(base); err != nil {
		service.setStatus(serviceName(service.cfg, "overfin", "Overfin"), true, false, err)
		service.mu.Lock()
		service.reliability.Overfin = "stale"
		service.reliability.Valuation = false
		service.reliability.Message = "Reading Overfin's scores failed; automatic removal planning is paused."
		service.mu.Unlock()
		return err
	}
	now := time.Now()
	for i := range base {
		base[i].OverfinEnrichedAt = now
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	items := cloneMedia(service.items)
	mergeOverfinFacts(items, base)
	valuation.ApplyMedia(items, service.cfg, service.imdbRatings)
	if service.db != nil {
		if err := service.db.PublishEnrichment("overfin", service.generation, items); err != nil {
			service.reliability.Overfin = "stale"
			service.reliability.Valuation = false
			return err
		}
	}
	service.items = items
	service.reliability.Overfin = "reliable"
	service.reliability.Valuation = service.reliability.valuationReady()
	if service.reliability.Valuation {
		service.reliability.Message = "Media valuation is reliable."
	} else {
		service.reliability.Message = "Other enrichment is stale; automatic removal planning is paused."
	}
	service.setStatusLocked(serviceName(service.cfg, "overfin", "Overfin"), true, true, nil)
	return nil
}

// preserveOverfinFacts mirrors preserveSeerrFacts for Overfin's scores.
func preserveOverfinFacts(dst, previous []model.Media) {
	byKey := make(map[string]model.Media, len(previous))
	for _, m := range previous {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		old, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]
		if !ok || externalIDsChanged(dst[i], old) {
			continue
		}
		dst[i].OverfinScore = old.OverfinScore
		dst[i].OverfinSeasonScores = old.OverfinSeasonScores
		dst[i].OverfinEnrichedAt = old.OverfinEnrichedAt
	}
}

func mergeOverfinFacts(dst, enriched []model.Media) {
	byKey := make(map[string]model.Media, len(enriched))
	for _, m := range enriched {
		byKey[fmt.Sprintf("%s:%s:%d", m.Type, m.ServiceID, m.SourceID)] = m
	}
	for i := range dst {
		if source, ok := byKey[fmt.Sprintf("%s:%s:%d", dst[i].Type, dst[i].ServiceID, dst[i].SourceID)]; ok {
			dst[i].OverfinScore = source.OverfinScore
			dst[i].OverfinSeasonScores = source.OverfinSeasonScores
			dst[i].OverfinEnrichedAt = source.OverfinEnrichedAt
		}
	}
}
