package main

import (
	"context"
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
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/governor"
	"redcyberfox/agent/internal/health"
	"redcyberfox/agent/internal/identity"
	"redcyberfox/agent/internal/ingestion"
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
	flag.Parse()

	log.Println("Starting Endpoint Agent Alpha...")

	// 1. Config Loading & Validation
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// 2. Identity Initialization
	id, err := identity.LoadOrInitialize(*identityPath)
	if err != nil {
		log.Fatalf("Failed to initialize device identity: %v", err)
	}
	log.Printf("Device Identity: %s", id.DeviceID)

	// 3. Graceful Shutdown Context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	drainCtx, drainCancel := context.WithCancel(context.Background())
	defer drainCancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Received termination signal, shutting down...")
		cancel()

		// Establish one global provisional deterministic drain bound for shutdown
		go func() {
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				drainCancel() // will surface as context.Canceled for drainCtx
			case <-drainCtx.Done():
			}
		}()
	}()

	// 4. Storage Initialization
	db := storage.NewSQLiteStorage(agentDBPath, *identityPath, storageQuotaBytes)
	if err := db.Init(ctx); err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// 5. Forwarder Initialization
	fwd := forwarder.NewForwarder(cfg.APIAddr, cfg.TenantID, db)
	if err := fwd.Start(ctx); err != nil {
		log.Fatalf("Failed to start forwarder: %v", err)
	}

	// 6. Collector Initialization
	centralEvents := make(chan *events.CanonicalEvent, 200)

	// Phase 4.3.4: sample real process resources and the bounded ingress queue.
	// These thresholds are provisional observations until low-spec benchmarks
	// calibrate enforcement policy in a later Phase 4 slice.
	var monitorWg sync.WaitGroup
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
			coordinator, coordinatorErr := governor.NewCoordinator(resourceProvider, resourceGovernor, resourceMetrics,
				governor.CoordinatorConfig{StorageBudgetBytes: uint64(storageQuotaBytes)}, time.Now)
			if coordinatorErr != nil {
				log.Printf("Resource coordination unavailable: %v", coordinatorErr)
			} else {
				monitorWg.Add(1)
				go func() {
					defer monitorWg.Done()
					ticker := time.NewTicker(5 * time.Second)
					defer ticker.Stop()
					var lastError string
					var samples uint64
					_ = governor.RunSampling(ctx, ticker.C, coordinator, func() governor.RuntimeSignals {
						return governor.RuntimeSignals{
							QueueDepth:    governor.ValidUint64{Value: uint64(len(centralEvents)), Valid: true},
							QueueCapacity: governor.ValidUint64{Value: uint64(cap(centralEvents)), Valid: true},
							// The channel cannot reveal its oldest element without consuming it.
							QueueOldestAgeSec: governor.ValidFloat64{Valid: false},
							NetworkState:      fwd.NetworkState(),
						}
					}, func(result governor.StepResult, stepErr error) {
						if ctx.Err() != nil {
							return
						}
						currentError := ""
						if stepErr != nil {
							currentError = stepErr.Error()
						}
						if currentError != lastError {
							if currentError == "" {
								log.Println("Resource sampling recovered")
							} else {
								log.Printf("Resource sampling degraded: %s", currentError)
							}
							lastError = currentError
						}
						samples++
						if samples%12 == 0 && result.Ready {
							snapshot := resourceMetrics.Snapshot()
							log.Printf("Resource sample: state=%s cpu=%v rss=%v storage=%v queue=%v network=%s",
								snapshot.GovernorState, snapshot.AgentCPUPercent, snapshot.AgentRSSBytes,
								snapshot.DiskStorageUsedBytes, snapshot.QueueSaturationPercent, result.Signals.NetworkState)
						}
					})
				}()
			}
		}
	}

	procCol := process.NewCollector(cfg)
	log.Println("Starting Process Collector...")
	if err := procCol.Start(ctx, centralEvents); err != nil {
		log.Fatalf("Process collector failed to start: %v", err)
	}

	fsCol := filesystem.NewCollector(cfg)
	log.Println("Starting Filesystem Collector...")
	if err := fsCol.Start(ctx, centralEvents); err != nil {
		log.Fatalf("Filesystem collector failed to start: %v", err)
	}

	invCol := inventory.NewCollector(cfg)
	log.Println("Starting Inventory Collector...")
	if err := invCol.Start(ctx, centralEvents); err != nil {
		log.Fatalf("Inventory collector failed to start: %v", err)
	}

	netCol := network.NewCollector(cfg)
	log.Println("Starting Network Collector...")
	if err := netCol.Start(ctx, centralEvents); err != nil {
		log.Fatalf("Network collector failed to start: %v", err)
	}

	usbCol := usb.NewCollector(cfg)
	log.Println("Starting USB Collector...")
	if err := usbCol.Start(ctx, centralEvents); err != nil {
		log.Fatalf("USB collector failed to start: %v", err)
	}

	// 7. Health Manager (from previous phase)
	healthCh := make(chan *health.Signal, 100)
	healthMgr := health.NewManager(id.DeviceID, cfg.TenantID, cfg.SiteID, healthCh)
	go func() {
		if err := healthMgr.Start(ctx); err != nil {
			log.Printf("Health manager stopped with error: %v", err)
		}
	}()

	// 7.5 Phase 3 Detection Core
	log.Println("Production detection rule set pending real IOC/rule gate")
	detEngine := detection.NewEngine(db, []detection.Rule{})

	// 8. Central Event Ingestion Loop (uses shared production Ingestion Engine)
	ingestEngine := ingestion.NewEngine(db, ingestion.Config{
		TenantID: cfg.TenantID,
		SiteID:   cfg.SiteID,
		AssetID:  id.DeviceID,
		OnCommitted: func(ctx context.Context, ev *events.CanonicalEvent) {
			detEngine.Evaluate(ctx, ev)
			fwd.Wakeup()
		},
	})

	var ingestWg sync.WaitGroup
	ingestWg.Add(1)
	go func() {
		defer ingestWg.Done()
		ingestEngine.Run(ctx, drainCtx, centralEvents)
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
	monitorWg.Wait()

	// Shutdown Sequence
	// 1. Stop Collectors (stops OS collection, waits for collector to exit)
	procCol.Stop()
	fsCol.Stop()
	invCol.Stop()
	netCol.Stop()
	usbCol.Stop()

	// 2. Close centralEvents channel to drain the ingestion loop
	close(centralEvents)

	// 3. Wait for ingestion loop to finish draining and exit
	ingestWg.Wait()

	// 4. Stop Forwarder (waits for forwarding loop to exit)
	fwd.Stop()

	// 5. Close Storage
	db.Close()

	log.Println("Endpoint Agent shutdown cleanly.")
}
