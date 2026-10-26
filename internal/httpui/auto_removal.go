package httpui

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/overfin/overtide/internal/cleanup"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/model"
	"github.com/overfin/overtide/internal/store"
	"github.com/overfin/overtide/internal/tasks"
)

const autoRemovalTaskID = "auto-removal"
const autoRemovalEvalInterval = 15 * time.Minute

// runAutoRemovalEvaluation evaluates every known storage device's
// cross-domain Cleanup plan. In config.RemovalAutoDisabled (the default,
// and whatever an empty/invalid value falls back to) it does nothing at
// all — no snapshot, no cleanup.Build — not just withholds submission.
// config.RemovalAutoConfirm still runs the full evaluation, applying every
// filter below, but never submits: the results are only ever surfaced by
// the normal manual removal flow (the Storage page), left for a human to
// act on. Only config.RemovalAutoAuto actually submits. Every per-service
// opt-in (Service.AllowAutomaticRemoval) still defaults to false regardless
// of mode, so a fresh or upgraded install touches nothing until services
// are individually opted in too. Unassociated torrents are additionally
// excluded by default even in Auto mode, since Overtide has no
// owning-media relationship to judge them safe to delete unattended — see
// actionIsUnassociatedTorrent.
func (server *Server) runAutoRemovalEvaluation(ctx context.Context) error {
	if server.inv == nil || server.tasks == nil {
		return nil
	}
	cfg := server.inv.Config()
	if cfg.Removal.AutoMode != config.RemovalAutoConfirm && cfg.Removal.AutoMode != config.RemovalAutoAuto {
		return nil
	}
	items, _, err := server.inv.Snapshot()
	if err != nil {
		log.Printf("[auto-removal] skipped this cycle: snapshot unavailable: %v", err)
		return nil
	}
	if server.askOverfinIfNeeded(cfg, items) {
		return nil // planned at the next cycle, with Overfin's answer
	}
	torrents := server.inv.TorrentSnapshot()
	reliable := server.planningReliable(server.inv.ReliabilitySnapshot())
	mediaByDevice := server.inv.MediaByDevice(items)
	torrentsByDevice := server.inv.TorrentsByDevice(torrents)
	for _, device := range server.inv.StorageDevices() {
		if !cfg.AutomaticRemovalEnabledFor(device.RepresentativePath) {
			continue
		}
		target, critical := cfg.ThresholdsFor(device.RepresentativePath)
		plan, err := cleanup.Build(device.RepresentativePath, device.OtherBytes, target, critical, mediaByDevice[device.RepresentativePath], torrentsByDevice[device.RepresentativePath], reliable, nil, cfg.Removal.CopiesWithTitles)
		if err != nil || !plan.Available {
			continue
		}
		for _, group := range plan.Actions {
			if actionIsUnassociatedTorrent(group) && !cfg.Removal.AutoRemoveUnassociatedTorrents {
				continue
			}
			if actionIsIncompleteTorrent(group) && !cfg.Removal.AutoRemoveIncompleteTorrents {
				continue
			}
			if cfg.Removal.AutoMode != config.RemovalAutoAuto {
				// Confirm mode: this candidate has passed every automatic-
				// removal filter, but nothing gets submitted — a human
				// still acts through the normal manual removal flow.
				continue
			}
			if err := server.queueCleanup(group, cfg, "Automatic cleanup: "); err != nil {
				log.Printf("[auto-removal] skipped action kind=%s: %v", group.Kind, err)
			}
		}
	}
	return nil
}

// overfinValuesFresh is how old Overfin's values may be when a disk needs
// room before Overfin is asked again.
const overfinValuesFresh = 15 * time.Minute

// askOverfinIfNeeded asks Overfin for the titles' values (its task, run
// now) when a disk with automatic removal is over its target and the
// values it gave are older than overfinValuesFresh: Overtide asks what to
// remove when it needs space, not on a schedule. It reports whether it
// asked.
func (server *Server) askOverfinIfNeeded(cfg config.Config, items []model.Media) bool {
	if cfg.Overfin.URL == "" {
		return false
	}
	over := false
	for _, device := range server.inv.StorageDevices() {
		usable := device.UsableBytes()
		if !cfg.AutomaticRemovalEnabledFor(device.RepresentativePath) || usable == 0 {
			continue
		}
		target, _ := cfg.ThresholdsFor(device.RepresentativePath)
		if float64(device.OvertideUsedBytes())/float64(usable)*100 >= target {
			over = true
		}
	}
	if !over {
		return false
	}
	var newest time.Time
	for _, m := range items {
		if m.OverfinEnrichedAt.After(newest) {
			newest = m.OverfinEnrichedAt
		}
	}
	if time.Since(newest) < overfinValuesFresh {
		return false
	}
	if _, err := server.tasks.Submit(tasks.Request{TaskID: "overfin", Kind: tasks.TriggerEvent, Priority: tasks.PriorityManual, Cause: "A disk is over its target"}); err != nil {
		log.Printf("[auto-removal] asking Overfin for values: %v", err)
		return false
	}
	return true
}

// actionIsUnassociatedTorrent reports whether action is a StandaloneTorrent
// candidate Overtide has no relationship for at all, current or historical.
// Superseded and Orphaned torrents both carry known import provenance (a
// specific replacement, or a former relationship whose media is simply gone)
// and are not excluded by this gate. Unassociated has none of that — it may
// simply be something the user downloaded through that client for their own
// purposes, or from a service Overtide doesn't track; automatic removal has
// no basis to judge those safe to delete unattended.
func actionIsUnassociatedTorrent(action cleanup.Action) bool {
	if action.Kind != cleanup.StandaloneTorrent || len(action.Torrents) == 0 {
		return false
	}
	// A cross-seed group has a known owner if any of its torrents does.
	for _, t := range action.Torrents {
		if model.NormalizeTorrentStatus(t.AssociationStatus) != model.TorrentUnassociated {
			return false
		}
	}
	return true
}

// actionIsIncompleteTorrent reports whether action is a StandaloneTorrent
// candidate that hasn't finished downloading yet. This only controls
// whether such a torrent can be an automatic-removal candidate at all —
// whether it's actually selected still depends on it scoring low on
// Torrent Value, which an actively downloading torrent won't, since its
// Recent activity already reflects that.
func actionIsIncompleteTorrent(action cleanup.Action) bool {
	if action.Kind != cleanup.StandaloneTorrent || len(action.Torrents) == 0 {
		return false
	}
	for _, t := range action.Torrents {
		if t.AmountLeftBytes > 0 {
			return true
		}
	}
	return false
}

// cleanupStillEligible checks, just before a removal from the cleanup plan
// runs, that what it removes is still a candidate: the title not kept or
// otherwise protected, no torrent with an obligation, every service still
// allowing removal. Things change between a click and the run (a Keep, a
// tracker's seeding time), and the plan was only a snapshot.
func (server *Server) cleanupStillEligible(form url.Values) error {
	torrents := map[string]model.Torrent{}
	for _, t := range server.inv.TorrentSnapshot() {
		torrents[strings.ToLower(t.Hash)] = t
	}
	checkTorrent := func(hash string, lone bool) error {
		t, ok := torrents[strings.ToLower(strings.TrimSpace(hash))]
		switch {
		case !ok:
			return fmt.Errorf("torrent %s is gone", hash)
		case t.RemovalRestricted:
			return fmt.Errorf("%s: its service no longer allows removal", t.Name)
		case t.Obligation != "":
			return fmt.Errorf("%s: %s", t.Name, t.Obligation)
		case lone && t.Protected:
			return fmt.Errorf("%s: %s", t.Name, t.ProtectionReason)
		}
		return nil
	}
	switch form.Get("kind") {
	case "media":
		id, _ := strconv.Atoi(form.Get("media_id"))
		items, _, _ := server.inv.Snapshot()
		m, ok := mediaFor(items, model.MediaType(form.Get("media_type")), id, form.Get("service_id"))
		switch {
		case !ok:
			return fmt.Errorf("the title is gone")
		case m.RemovalRestricted:
			return fmt.Errorf("%s: its service no longer allows removal", m.Title)
		case m.Protected:
			return fmt.Errorf("%s: %s", m.Title, m.ProtectionReason)
		}
		for _, hash := range form["torrent"] {
			if err := checkTorrent(hash, false); err != nil {
				return err
			}
		}
	case "torrent":
		return checkTorrent(form.Get("hash"), true)
	}
	return nil
}

func mediaFor(items []model.Media, kind model.MediaType, id int, serviceID string) (model.Media, bool) {
	for _, m := range items {
		if m.Type == kind && m.SourceID == id && (serviceID == "" || m.ServiceID == serviceID) {
			return m, true
		}
	}
	return model.Media{}, false
}

// expandGroup turns a cross-seed group (a lone-torrent action with several
// torrents sharing files) into one action per torrent, submitted together:
// each removal deletes its own links, and the last one frees the data.
// Any other action comes back as it is.
func expandGroup(action cleanup.Action) []cleanup.Action {
	if action.Kind != cleanup.StandaloneTorrent || len(action.Torrents) < 2 {
		return []cleanup.Action{action}
	}
	singles := make([]cleanup.Action, 0, len(action.Torrents))
	for _, t := range action.Torrents {
		single := action
		single.Torrents = []model.Torrent{t}
		singles = append(singles, single)
	}
	return singles
}

// formForAction builds the exact url.Values shape a browser's manual removal
// confirmation would submit for the given Action, reusing the same form
// vocabulary admitRemoval/executeRemovalNowContext already expect. No new
// endpoint or field names are introduced.
func (server *Server) formForAction(action cleanup.Action) (url.Values, error) {
	form := url.Values{}
	switch action.Kind {
	case cleanup.StandaloneMedia, cleanup.StandaloneSeason, cleanup.HardlinkedBundle:
		refs, _, err := server.inv.ManagedFileRefs(action.Media.Type, action.Media.SourceID, action.Media.ServiceID)
		if err != nil {
			return nil, err
		}
		if action.Season != nil {
			scoped := refs[:0]
			for _, ref := range refs {
				if len(ref.Parts) > 0 && ref.Parts[0].Group == action.Season.FileGroup {
					scoped = append(scoped, ref)
				}
			}
			refs = scoped
		}
		if len(refs) == 0 {
			return nil, fmt.Errorf("media %s:%d has no managed files to select for this action", action.Media.Type, action.Media.SourceID)
		}
		form.Set("kind", "media")
		form.Set("media_type", string(action.Media.Type))
		form.Set("media_id", strconv.Itoa(action.Media.SourceID))
		form.Set("service_id", action.Media.ServiceID)
		for _, ref := range refs {
			form.Add("managed_file", managedFileKey(ref))
		}
		for _, t := range action.Torrents {
			form.Add("torrent", strings.ToLower(t.Hash))
		}
	case cleanup.StandaloneTorrent:
		if len(action.Torrents) == 0 {
			return nil, fmt.Errorf("torrent action has no torrent")
		}
		form.Set("kind", "torrent")
		form.Set("hash", strings.ToLower(action.Torrents[0].Hash))
		form.Set("service_id", action.Torrents[0].ServiceID)
		form.Set("target", "1")
	default:
		return nil, fmt.Errorf("unsupported action kind %q", action.Kind)
	}
	return form, nil
}

// queueCleanup queues the removals of one cleanup plan row: one removal,
// or one per torrent for a cross-seed group. Every removal is admitted
// before any is queued, so a group goes whole or not at all. They go
// through the same admission, history and scheduler as a removal from the
// browser, and are checked again just before running (cleanupStillEligible).
// cause tells automatic and manual cleanup apart in history and tasks.
func (server *Server) queueCleanup(row cleanup.Action, cfg config.Config, cause string) error {
	database := server.inv.Store()
	if database == nil {
		return fmt.Errorf("durable operation history unavailable")
	}
	type admitted struct {
		form       url.Values
		descriptor removalAdmission
	}
	var removals []admitted
	for _, action := range expandGroup(row) {
		form, err := server.formForAction(action)
		if err != nil {
			return err
		}
		// Admission refuses these fields on a bare torrent removal.
		if action.Kind != cleanup.StandaloneTorrent {
			if cfg.Removal.AutoExcludeFromImportLists {
				form.Set("exclude_movies", "1")
				form.Set("exclude_series", "1")
			}
		}
		descriptor, err := server.admitRemoval(form)
		if err != nil {
			return fmt.Errorf("admission rejected (the plan may have changed): %w", err)
		}
		removals = append(removals, admitted{form, descriptor})
	}
	for _, removal := range removals {
		if err := server.queueRemoval(database, removal.form, removal.descriptor, cause); err != nil {
			return err
		}
	}
	return nil
}

// queueRemoval records an admitted removal in history, then schedules it.
func (server *Server) queueRemoval(database *store.Store, form url.Values, descriptor removalAdmission, cause string) error {
	command := scheduledRemovalCommand{Form: cloneForm(form), DryRun: descriptor.DryRun, Cleanup: true}
	queuedPayload, err := json.Marshal(queuedRemovalPayload{Command: command})
	if err != nil {
		return err
	}
	historyID, err := database.SaveHistoryEvent(store.HistoryEvent{EventType: "removal", Status: "queued", DryRun: descriptor.DryRun, RequestedKind: string(descriptor.Kind), RequestedKey: descriptor.Key, RequestedLabel: descriptor.Label, ServiceName: descriptor.ServiceName, Payload: queuedPayload})
	if err != nil {
		return fmt.Errorf("record operation before scheduling: %w", err)
	}
	command.HistoryID = historyID
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	_, err = server.tasks.Submit(tasks.Request{TaskID: removalTaskID, Kind: tasks.TriggerEvent, Priority: tasks.PriorityMutation, Durable: true, CoalescingKey: fmt.Sprintf("operation:%d", historyID), Cause: cause + descriptor.Label, Payload: payload})
	return err
}
