package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"redcyberfox/agent/internal/collectors/filesystem"
	"redcyberfox/agent/internal/collectors/inventory"
	"redcyberfox/agent/internal/collectors/network"
	"redcyberfox/agent/internal/collectors/process"
	"redcyberfox/agent/internal/collectors/usb"
	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/detection"
	"redcyberfox/agent/internal/eventqueue"
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/governor"
	"redcyberfox/agent/internal/health"
	"redcyberfox/agent/internal/ingestion"
	"redcyberfox/agent/internal/interfaces"
	"redcyberfox/agent/internal/resourcereport"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

const (
	agentDBPath       = "agent.db"
	storageQuotaBytes = int64(50 * 1024 * 1024)
)

func main() {
	configPath := flag.String("config", "agent.json", "path to config file")
	identityPath := flag.String("identity", "identity.json", "path to identity file")
	measureReport := flag.String("measure-report", "", "path to machine-readable measurement report JSON (empty disables report writing)")
	measureProfile := flag.String("measure-profile", "", "description of hardware or workload profile for report manifest")
	measureDuration := flag.Duration("measure-duration", 0, "run bounded resource measurement for duration, then exit (0 disables measurement mode)")
	measureInterval := flag.Duration("measure-interval", 2*time.Second, "sampling interval for measurement mode")
	flag.Parse()

	log.Println("Starting Endpoint Agent Alpha...")

	// 1. Config Loading & Validation
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	reportCfg := resourcereport.Config{
		ReportPath:         *measureReport,
		Profile:            *measureProfile,
		Duration:           *measureDuration,
		Interval:           *measureInterval,
		DBPath:             agentDBPath,
		IdentityPath:       *identityPath,
		ConfigPath:         *configPath,
		EmergencySpillPath: "agent_emergency_spill.log",
		WatchedPaths:       cfg.MonitorPaths,
	}
	if err := resourcereport.ValidateConfig(reportCfg); err != nil {
		log.Fatalf("Invalid measurement report configuration: %v", err)
	}

	// 2. Storage Initialization & Authoritative Device Identity
	// SQLite is the single authoritative source of truth for device identity.
	// If a legacy identity file is present, db.Init migrates it into SQLite and removes it.
	db := storage.NewSQLiteStorage(agentDBPath, *identityPath, storageQuotaBytes)
	if err := db.Init(context.Background()); err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	deviceID := db.GetDeviceID()
	if deviceID == "" {
		log.Fatalf("Failed to establish authoritative device identity from storage")
	}
	log.Printf("Device Identity: %s", deviceID)

	var initialPendingEvents *uint64
	if rawDB := db.GetDB(); rawDB != nil {
		var cnt uint64
		if err := rawDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&cnt); err == nil {
			initialPendingEvents = &cnt
		}
	}

	// 3. Graceful Shutdown & Shared Drain Coordination
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	shutdownCoord := resourcereport.NewShutdownCoordinator(cancel, drainCancel, 10*time.Second)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		shutdownCoord.Trigger("os_signal: " + sig.String())
	}()

	// 4. Forwarder Initialization
	fwd := forwarder.NewForwarder(cfg.APIAddr, cfg.TenantID, db)
	if err := fwd.Start(ctx); err != nil {
		log.Fatalf("Failed to start forwarder: %v", err)
	}

	// 5. Instrumented Event Queue & Detection Core
	eventQ := eventqueue.New(200)

	// Phase 3 Detection Core (Active Deterministic Rules)
	activeRules := []detection.Rule{
		&detection.RealIOCExecRule{},
	}
	log.Printf("Production detection engine initialized with %d active deterministic rules", len(activeRules))
	detEngine := detection.NewEngine(db, activeRules)

	// 6. Central Event Ingestion Engine (uses shared production Ingestion Engine)
	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID: cfg.TenantID,
		SiteID:   cfg.SiteID,
		AssetID:  deviceID,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
			fwd.Wakeup()
		},
	})

	// Phase 4.3: Initialize resource governance infrastructure (synchronous, on-demand).
	// Recurring endpoint background sampling is not enabled.
	// Storage pressure evaluation remains unresolved pending an authoritative persistence budget policy.
	var coordinator *governor.Coordinator
	var reporter *resourcereport.Reporter
	var measureJoin func() error = func() error { return nil }
	resourceProvider, err := governor.NewSQLiteResourceProvider(agentDBPath, time.Now)
	if err != nil {
		log.Printf("Resource monitoring unavailable: %v", err)
	} else {
		resourceMetrics := governor.NewGovernorMetrics(time.Now)
		resourceGovernor, governorErr := governor.NewGovernor(governor.Config{
			CPUSaverThreshold: 80, CPUEmergencyThreshold: 90, CPURecoveryMargin: 5,
			MemorySaverBytes: 200 * 1024 * 1024, MemoryEmergencyBytes: 300 * 1024 * 1024, MemoryRecoveryMarginBytes: 10 * 1024 * 1024,
			StorageSaverRatio: 0.7, StorageEmergencyRatio: 0.9, StorageRecoveryMarginRatio: 0.05,
			QueueSaverRatio: 0.7, QueueEmergencyRatio: 0.9, QueueRecoveryMarginRatio: 0.05,
			RecoveryDuration: 30 * time.Second,
		}, time.Now, func(tr governor.Transition) {
			resourceMetrics.RecordTransition(tr)
			log.Printf("Resource state %s -> %s: %s", tr.PreviousState, tr.NewState, tr.Reason)
		})
		if governorErr != nil {
			log.Printf("Resource governor unavailable: %v", governorErr)
		} else {
			// Authoritative storage budget is unresolved; StorageBudgetBytes is 0 to ensure
			// storage pressure evaluation remains blocked rather than assuming arbitrary quotas.
			var coordErr error
			coordinator, coordErr = governor.NewCoordinator(resourceProvider, resourceGovernor, resourceMetrics,
				governor.CoordinatorConfig{StorageBudgetBytes: 0}, time.Now)
			if coordErr != nil {
				log.Printf("Resource coordinator unavailable: %v", coordErr)
			} else {
				// Initial startup baseline invocation to prime CPU provider continuity
				// and sample initial runtime state. This executes synchronously on demand
				// without background tickers, goroutines, or recurring scheduling.
				stepRes, stepErr := coordinator.Step(ctx, governor.RuntimeSignals{
					QueueDepth:        eventQ.Depth(),
					QueueCapacity:     eventQ.Capacity(),
					QueueOldestAgeSec: eventQ.OldestEventAge(),
					NetworkState:      fwd.NetworkState(),
				})
				if stepErr != nil && !errors.Is(stepErr, governor.ErrMissingStorageBudget) {
					log.Printf("Resource coordinator initial baseline sample error: %v", stepErr)
				}
				log.Printf("Resource governor coordinator initialized (on-demand mode, initial step ready=%v evaluated=%v storageBlocked=%v)",
					stepRes.Ready, stepRes.Evaluated, stepRes.StorageBlocked)

				// Measurement Runner (bounded observational mode)
				if *measureDuration > 0 || *measureReport != "" {
					reportRules := make([]resourcereport.RuleInfo, 0, len(activeRules))
					for _, r := range activeRules {
						reportRules = append(reportRules, resourcereport.RuleInfo{
							RuleID:   r.ID(),
							Version:  r.Version(),
							Severity: r.Severity(),
						})
					}
					reporter = resourcereport.NewReporter(reportCfg, reportRules)
					log.Printf("Starting bounded measurement mode (duration: %v, interval: %v, report: %q)...",
						*measureDuration, *measureInterval, *measureReport)
					var measureWg sync.WaitGroup
					var measureErr error
					measureWg.Add(1)
					measureJoin = func() error {
						measureWg.Wait()
						return measureErr
					}

					go func() {
						defer measureWg.Done()
						if runErr := resourcereport.RunMeasurementRunner(
							ctx,
							reportCfg,
							reporter,
							coordinator,
							resourceMetrics,
							func() governor.RuntimeSignals {
								return governor.RuntimeSignals{
									QueueDepth:        eventQ.Depth(),
									QueueCapacity:     eventQ.Capacity(),
									QueueOldestAgeSec: eventQ.OldestEventAge(),
									NetworkState:      fwd.NetworkState(),
								}
							},
							shutdownCoord,
							func() (uint64, uint64) {
								return eventQ.BlockedEnqueueAttemptsTotal(), eventQ.PressureEpisodesTotal()
							},
						); runErr != nil {
							log.Printf("Measurement runner error: %v", runErr)
							measureErr = runErr
						}
					}()
				}
			}
		}
	}
	_ = coordinator

	procCol := process.NewCollector(cfg)
	log.Println("Starting Process Collector...")
	if err := procCol.Start(ctx, eventQ.In()); err != nil {
		log.Fatalf("Process collector failed to start: %v", err)
	}

	fsCol := filesystem.NewCollector(cfg)
	log.Println("Starting Filesystem Collector...")
	if err := fsCol.Start(ctx, eventQ.In()); err != nil {
		log.Fatalf("Filesystem collector failed to start: %v", err)
	}

	invCol := inventory.NewCollector(cfg)
	log.Println("Starting Inventory Collector...")
	if err := invCol.Start(ctx, eventQ.In()); err != nil {
		log.Fatalf("Inventory collector failed to start: %v", err)
	}

	netCol := network.NewCollector(cfg)
	log.Println("Starting Network Collector...")
	if err := netCol.Start(ctx, eventQ.In()); err != nil {
		log.Fatalf("Network collector failed to start: %v", err)
	}

	usbCol := usb.NewCollector(cfg)
	log.Println("Starting USB Collector...")
	if err := usbCol.Start(ctx, eventQ.In()); err != nil {
		log.Fatalf("USB collector failed to start: %v", err)
	}

	// 7. Health Manager (from previous phase)
	healthCh := make(chan *health.Signal, 100)
	healthMgr := health.NewManager(deviceID, cfg.TenantID, cfg.SiteID, healthCh)
	go func() {
		if err := healthMgr.Start(ctx); err != nil {
			log.Printf("Health manager stopped with error: %v", err)
		}
	}()

	var ingestWg sync.WaitGroup
	ingestWg.Add(1)
	go func() {
		defer ingestWg.Done()
		ingestEngine.Run(ctx, drainCtx, eventQ.Out())
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-healthCh:
				if !ok {
					return
				}
				log.Printf("Internal pipeline received health signal: Type=%s", sig.Type)
			}
		}
	}()

	// Wait for shutdown
	<-ctx.Done()
	log.Println("Endpoint Agent shutdown sequence initiated.")

	// Shutdown Sequence
	var collectors []interfaces.Stopper
	collectors = append(collectors, procCol, fsCol, invCol, netCol, usbCol)
	if healthMgr != nil {
		collectors = append(collectors, healthMgr)
	}

	shutdownErr := resourcereport.ExecuteShutdown(resourcereport.ShutdownParams{
		Context:              ctx,
		ShutdownCoord:        shutdownCoord,
		Collectors:           collectors,
		EventQueue:           eventQ,
		IngestWait:           &ingestWg,
		Forwarder:            fwd,
		MeasureJoin:          measureJoin,
		Storage:              db,
		RawDB:                db.GetDB(),
		InitialPendingEvents: initialPendingEvents,
		IngestionAccounting:  ingestEngine.Accounting,
		ForwarderAccounting:  fwd.Accounting,
		Reporter:             reporter,
	})

	if shutdownErr != nil {
		log.Printf("Endpoint Agent shutdown completed with errors: %v", shutdownErr)
		os.Exit(1)
	}

	log.Println("Endpoint Agent shutdown cleanly.")
}
