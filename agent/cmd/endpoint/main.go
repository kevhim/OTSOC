package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"redcyberfox/agent/internal/config"
	"redcyberfox/agent/internal/health"
	"redcyberfox/agent/internal/identity"
	"redcyberfox/pkg/events"
)

func main() {
	configPath := flag.String("config", "agent.json", "path to config file")
	identityPath := flag.String("identity", "identity.json", "path to identity file")
	flag.Parse()

	log.Println("Starting Endpoint Agent Alpha Phase 2A Foundation...")

	// 1. Identity Initialization (Crash-safe)
	id, err := identity.LoadOrInitialize(*identityPath)
	if err != nil {
		log.Fatalf("Failed to initialize device identity: %v", err)
	}
	log.Printf("Device Identity: %s", id.DeviceID)

	// 2. Config Loading
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

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

	// 4. Initialize Core Components (Mocks for Phase 2A)
	eventsCh := make(chan *events.CanonicalEvent, 100)

	healthMgr := health.NewManager(id.DeviceID, cfg.TenantID, cfg.SiteID, eventsCh)

	// Start components
	go func() {
		if err := healthMgr.Start(ctx); err != nil {
			log.Printf("Health manager stopped with error: %v", err)
		}
	}()

	// Consume events to simulate storage/forwarder
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-eventsCh:
				log.Printf("Internal pipeline received event: Category=%s Source=%s", ev.Category, ev.Source)
			}
		}
	}()

	// Wait for shutdown
	<-ctx.Done()
	log.Println("Endpoint Agent Alpha shutdown cleanly.")
}
