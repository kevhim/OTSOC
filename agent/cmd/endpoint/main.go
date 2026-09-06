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

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		log.Println("Received termination signal, shutting down...")
		cancel()
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

				// Validation Boundary
				// CanonicalEvent.Validate() requires EventID, which is assigned by Storage.
				// Validation is owned by the Server; Forwarder handles HTTP 400 by moving to DLQ.
				// Duplicating validation here is unnecessary and conflicts with Storage's EventID ownership.

				// Local Durability Boundary
				// We use a context with a timeout for Storage to prevent hanging the main loop if SQLite locks.
				storeCtx, storeCancel := context.WithTimeout(context.Background(), 2*time.Second)
				if err := db.Store(storeCtx, ev); err != nil {
					log.Printf("Storage error: failed to persist event %s: %v", ev.EventID, err)
					storeCancel()
					continue
				}
				storeCancel()

				// Forwarding Eligibility Boundary
				fwd.Wakeup()
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
