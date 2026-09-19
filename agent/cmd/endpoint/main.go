package main

import (
	"context"
	"encoding/json"
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
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/health"
	"redcyberfox/agent/internal/identity"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func main() {
	configPath := flag.String("config", "agent.json", "path to config file")
	identityPath := flag.String("identity", "identity.json", "path to identity file")
	flag.Parse()

	log.Println("Starting Endpoint Agent Alpha Phase 2C.5 Integration...")

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
	db := storage.NewSQLiteStorage("agent.db", *identityPath, 50*1024*1024)
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

	// 8. Central Event Ingestion Loop
	var ingestWg sync.WaitGroup
	ingestWg.Add(1)

	go func() {
		defer ingestWg.Done()

		for {
			select {
			case sig := <-healthCh:
				log.Printf("Internal pipeline received health signal: Type=%s", sig.Type)
			case ev, ok := <-centralEvents:
				if !ok {
					// centralEvents channel closed, drain complete, exit loop
					return
				}
				// Enrichment Boundary
				ev.TenantID = cfg.TenantID
				ev.SiteID = cfg.SiteID
				ev.AssetID = id.DeviceID

				// Local Durability Boundary
				storeCtx := ctx
				if ctx.Err() != nil {
					storeCtx = drainCtx
				}

				var storeErr error
				attempts := 0
				for {
					storeErr = db.Store(storeCtx, ev)

					// CASE 1: COMMITTED (Success)
					if storeErr == nil {
						break
					}

					// CASE 2: FAILED BEFORE COMMIT
					if errors.Is(storeErr, storage.ErrStoreFailedBeforeCommit) {
						if errors.Is(storeErr, context.Canceled) && storeCtx == ctx {
							// In-flight event interrupted by lifecycle cancellation.
							// Retry under global drain context using the SAME event_id.
							storeCtx = drainCtx
							continue
						} else if errors.Is(storeErr, context.Canceled) || errors.Is(storeErr, context.DeadlineExceeded) {
							// Drain deadline expired. Non-retryable. Explicit terminal handling.
							log.Printf("ERROR: Event %s explicitly dropped (global drain deadline expired): %v", ev.EventID, storeErr)
						} else {
							// Non-retryable cause (e.g. quota full)
							log.Printf("ERROR: Event %s explicitly dropped (non-retryable failure before commit): %v", ev.EventID, storeErr)
						}
						break
					}

					// CASE 3: UNCERTAIN COMMIT
					if errors.Is(storeErr, storage.ErrStoreUncertain) {
						attempts++
						// We use 3 attempts to ride out transient IO stutters.
						// Persistent uncertain errors beyond 3 attempts indicate severe disk/DB issues.
						if attempts >= 3 {
							log.Printf("CRITICAL: Event %s uncertain commit after 3 attempts. Attempting recovery.", ev.EventID)
							
							// Recovery metadata: move to DLQ. 
							// Bounded by active storeCtx so it cannot escape global shutdown deadline.
							dlqCtx, dlqCancel := context.WithTimeout(storeCtx, 2*time.Second)
							dlqErr := db.MoveToDLQ(dlqCtx, ev, "uncertain_commit", storeErr.Error())
							dlqCancel()
							
							if dlqErr == nil {
								log.Printf("INFO: Event %s successfully recovered to DLQ (metadata only).", ev.EventID)
								break
							}

							// DLQ failed. Best-effort emergency spill to disk.
							spillBytes, marshalErr := json.Marshal(ev)
							var spillErr error
							if marshalErr == nil {
								spillErr = os.WriteFile("agent_emergency_spill.log", append(spillBytes, '\n'), 0600)
							}

							if spillErr == nil {
								log.Printf("CRITICAL: Event %s spilled to disk (BEST-EFFORT) due to uncertain commit and DLQ failure.", ev.EventID)
								break
							}

							// Terminal behavior
							log.Fatalf("FATAL: Event %s uncertain commit, DLQ failed (%v), and emergency spill failed (%v). Terminating.", ev.EventID, dlqErr, spillErr)
						}
						
						// Cancellation-aware bounded wait
						timer := time.NewTimer(100 * time.Millisecond)
						select {
						case <-storeCtx.Done():
							timer.Stop()
							if storeCtx == ctx {
								storeCtx = drainCtx
								continue
							}
							log.Printf("ERROR: Event %s uncertain retry aborted due to global drain expiration.", ev.EventID)
							// Do not break here directly, let it fall out or loop to evaluate context error
						case <-timer.C:
						}

						if storeCtx.Err() != nil && storeCtx != ctx {
							break
						}
						continue
					}

					// UNKNOWN ERROR
					log.Printf("CRITICAL: Unknown storage error for event %s: %v", ev.EventID, storeErr)
					break
				}

				// Forwarding Eligibility Boundary
				if storeErr == nil {
					fwd.Wakeup()
				}
			}
		}
	}()

	// Wait for shutdown
	<-ctx.Done()
	log.Println("Endpoint Agent shutdown sequence initiated.")

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
