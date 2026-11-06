package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/overfin/overtide/internal/adding"
	"github.com/overfin/overtide/internal/applog"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/httpui"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/overswellimport"
	"github.com/overfin/overtide/internal/pacing"
	"github.com/overfin/overtide/internal/product"
	"github.com/overfin/overtide/internal/store"
	"github.com/overfin/overtide/internal/tasks"
)

const (
	databasePath              = "/config/state/inventory.db"
	logDirectory              = "/config/log"
	logRetentionDays          = 10
	fileReconcileInterval     = 12 * time.Hour
	enrichmentRemovalCooldown = 30 * time.Minute
)

func main() {
	configPath := flag.String("config", "/config/config.json", "path to config file")
	importFrom := flag.String("import-overswell", "", "Overswell's config folder (config.json, state/overswell.db): import its settings and state once, then exit; Overtide and Overswell stopped")
	flag.Parse()
	applicationLog, err := applog.OpenDaily(logDirectory, "overtide", logRetentionDays, time.Now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Overtide cannot start without its persistent application log: %v\n", err)
		os.Exit(1)
	}
	defer applicationLog.Close()
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	log.SetOutput(io.MultiWriter(os.Stdout, applicationLog))
	log.Printf("[app] starting %s %s", product.Name, product.Version)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[app] config: %v", err)
	}

	db, err := store.Open(databasePath)
	if err != nil {
		log.Fatalf("[app] database: %v", err)
	}
	defer db.Close()
	if *importFrom != "" {
		imported, report, err := overswellimport.Import(cfg, db, *importFrom)
		if err != nil {
			log.Fatalf("[import] %v", err)
		}
		if err := config.Save(*configPath, imported); err != nil {
			log.Fatalf("[import] save the settings: %v", err)
		}
		for _, line := range report {
			log.Printf("[import] %s", line)
		}
		log.Printf("[import] done: start Overtide, and leave Overswell stopped")
		return
	}
	inv := inventory.New(cfg, db)
	inv.SetConfigPath(*configPath)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		select {
		case loggingError := <-applicationLog.Errors():
			fmt.Fprintf(os.Stderr, "Overtide is stopping because its mandatory application log failed: %v\n", loggingError)
			cancel()
		case <-ctx.Done():
		}
	}()

	maintenanceClaim := tasks.ResourceClaim{Resource: "owner-filesystem-mutation", Mode: tasks.ClaimShared}
	publicationClaim := tasks.ResourceClaim{Resource: "inventory-publication", Mode: tasks.ClaimExclusive}
	retryPolicy := tasks.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Minute, MaxDelay: 15 * time.Minute}
	retryable := func(runner tasks.Runner) tasks.Runner {
		return func(ctx context.Context) error { return tasks.Retryable(runner(ctx)) }
	}
	fullScanAttachCompatible := func(active []tasks.TriggerKind, requested tasks.TriggerKind) bool {
		if requested == tasks.TriggerWorkflow {
			return true
		}
		for _, kind := range active {
			if kind != tasks.TriggerWorkflow {
				return true
			}
		}
		return false
	}
	taskManager, err := tasks.NewPersistent(db,
		tasks.Definition{ID: "inventory", Name: "Base inventory", Description: "Refresh Radarr, Sonarr, qBittorrent and import provenance.", Interval: cfg.RefreshInterval, Preflight: retryable(inv.ValidateReconciliation), AttachCompatible: fullScanAttachCompatible, Runner: retryable(func(ctx context.Context) error {
			var err error
			if tasks.TriggeredOnlyBy(ctx, tasks.TriggerWorkflow) {
				err = inv.RefreshAfterMutation(ctx)
			} else {
				err = inv.Refresh(ctx)
			}
			return err
		}), Advisory: inv.RefreshAdvisory, Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "overfin", Name: "Overfin values", Description: "Ask Overfin the value of each film and season the libraries have: at startup, and before every cleanup plan.", Preflight: retryable(inv.ValidateOverfin), Runner: retryable(inv.RefreshOverfin), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Interruptible: true, InterruptionDelay: enrichmentRemovalCooldown, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "files", Name: "File reconciliation", Description: "Scan service storage and reconcile file ownership.", Interval: fileReconcileInterval, Preflight: retryable(inv.ValidateReconciliation), AttachCompatible: fullScanAttachCompatible, Runner: retryable(func(ctx context.Context) error {
			if tasks.TriggeredOnlyBy(ctx, tasks.TriggerWorkflow) {
				return inv.ReconcileFilesAfterMutation(ctx)
			}
			return inv.ReconcileFiles(ctx)
		}), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
	)
	if err != nil {
		log.Fatalf("[scheduler] initialization: %v", err)
	}
	// Adding: Overfin's suggestions and wishes read every hour, the titles
	// tried every 15 minutes. One adding task at a time, and never while a
	// removal changes the files (adding imports, which moves them).
	pacer := pacing.New(inv.ProwlarrClient)
	adder := adding.New(inv)
	adder.Ready = pacer.Ready
	adder.Trackers = pacer.Unavailable
	adder.Room = adding.DiskRoom(inv.Room)
	addingClaims := []tasks.ResourceClaim{{Resource: "adding", Mode: tasks.ClaimExclusive}, maintenanceClaim}
	for _, definition := range []tasks.Definition{
		{ID: "adding-overfin", Name: "Overfin's suggestions", Description: "Read what Overfin suggests adding and what people wished for, into the waiting list.", Interval: adding.ReadEvery, Runner: adder.ReadOverfin, Resources: addingClaims, Priority: tasks.PriorityPeriodic},
		// A title Radarr or Sonarr just added (their events) waits for a
		// run that starts after it, not one already past taking it.
		{ID: "adding", Name: "Try the titles", Description: "Follow what's downloading, then search for the waiting titles, within the run's searches and the trackers' limits.", Interval: adding.RunEvery, Runner: adder.Run, Resources: addingClaims, Priority: tasks.PriorityPeriodic,
			AttachCompatible: func(_ []tasks.TriggerKind, requested tasks.TriggerKind) bool { return requested != tasks.TriggerEvent }},
	} {
		if err := taskManager.Register(definition); err != nil {
			log.Fatalf("[scheduler] adding registration: %v", err)
		}
	}
	if err := taskManager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Name: "Inventory and files consistency", Description: "Restore authoritative inventory and file topology after a mutation (a removal, or a service being added/edited/removed).", Steps: []string{"inventory", "files"}}); err != nil {
		log.Fatalf("[scheduler] workflow registration: %v", err)
	}

	ui, err := httpui.New(inv, taskManager)
	if err != nil {
		log.Fatalf("[http] initialize UI: %v", err)
	}
	ui.SetAdding(adder, pacer)
	ui.Start(ctx)
	go taskManager.Start(ctx)
	go func() {
		inventoryTrigger, triggerErr := taskManager.Submit(tasks.Request{TaskID: "inventory", Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		if triggerErr != nil {
			log.Printf("[scheduler] initial base inventory could not be submitted: %v", triggerErr)
			return
		}
		if _, err := taskManager.Await(ctx, inventoryTrigger.TriggerID); err != nil {
			log.Printf("[inventory] initial base inventory failed: %v", err)
		}
		if !inv.WaitForBase(ctx) {
			return
		}
		// File topology is independent of Overfin's scores and runs alongside them.
		_, _ = taskManager.Submit(tasks.Request{TaskID: "files", Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		// Overfin's scores are read again at once: a restart may follow a
		// long gap.
		_, _ = taskManager.Submit(tasks.Request{TaskID: "overfin", Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		// Adding starts from the inventory just read: suggestions first,
		// then a run.
		for _, id := range []string{"adding-overfin", "adding"} {
			_, _ = taskManager.Submit(tasks.Request{TaskID: id, Kind: tasks.TriggerStartup, Priority: tasks.PriorityManual, Durable: false, Cause: "Application startup"})
		}
	}()

	runError := httpui.Run(ctx, cfg.Server.Listen, ui.Handler())
	cancel()
	<-taskManager.Done()
	if loggingError := applicationLog.Err(); loggingError != nil {
		fmt.Fprintf(os.Stderr, "Overtide terminated after mandatory application log failure: %v\n", loggingError)
		os.Exit(1)
	}
	if runError != nil {
		log.Fatalf("[http] server: %v", runError)
	}
}
