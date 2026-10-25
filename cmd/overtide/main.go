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

	"github.com/overfin/overtide/internal/applog"
	"github.com/overfin/overtide/internal/config"
	"github.com/overfin/overtide/internal/httpui"
	"github.com/overfin/overtide/internal/inventory"
	"github.com/overfin/overtide/internal/product"
	"github.com/overfin/overtide/internal/store"
	"github.com/overfin/overtide/internal/tasks"
)

const (
	databasePath          = "/config/state/inventory.db"
	logDirectory          = "/config/log"
	logRetentionDays      = 10
	fileReconcileInterval = 12 * time.Hour
	// overfinScoresInterval: Overfin's scores move with what people do,
	// slowly; half an hour keeps them close without asking too often.
	overfinScoresInterval     = 30 * time.Minute
	enrichmentRemovalCooldown = 30 * time.Minute
)

func main() {
	configPath := flag.String("config", "/config/config.json", "path to config file")
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
		tasks.Definition{ID: "overfin", Name: "Overfin scores", Description: "Read each film's and season's value from Overfin, used as Retention Value.", Interval: overfinScoresInterval, Preflight: retryable(inv.ValidateOverfin), Runner: retryable(inv.RefreshOverfin), Resources: []tasks.ResourceClaim{maintenanceClaim, publicationClaim}, Interruptible: true, InterruptionDelay: enrichmentRemovalCooldown, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
		tasks.Definition{ID: "files", Name: "File reconciliation", Description: "Scan service storage and reconcile file ownership.", Interval: fileReconcileInterval, Preflight: retryable(inv.ValidateReconciliation), AttachCompatible: fullScanAttachCompatible, Runner: retryable(func(ctx context.Context) error {
			if tasks.TriggeredOnlyBy(ctx, tasks.TriggerWorkflow) {
				return inv.ReconcileFilesAfterMutation(ctx)
			}
			return inv.ReconcileFiles(ctx)
		}), Resources: []tasks.ResourceClaim{maintenanceClaim}, Priority: tasks.PriorityPeriodic, Retry: retryPolicy, Recovery: tasks.RecoveryRetry},
	)
	if err != nil {
		log.Fatalf("[scheduler] initialization: %v", err)
	}
	if err := taskManager.RegisterWorkflow(tasks.WorkflowDefinition{ID: "inventory-and-files-consistency", Name: "Inventory and files consistency", Description: "Restore authoritative inventory and file topology after a mutation (a removal, or a service being added/edited/removed).", Steps: []string{"inventory", "files"}}); err != nil {
		log.Fatalf("[scheduler] workflow registration: %v", err)
	}

	ui, err := httpui.New(inv, taskManager)
	if err != nil {
		log.Fatalf("[http] initialize UI: %v", err)
	}
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
