package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"redcyberfox/internal/api"
	"redcyberfox/internal/config"
	"redcyberfox/internal/queue"
)

func main() {
	log.Println("Starting RedCyberFox API Server (Phase 1 Vertical Slice)")

	cfg := config.LoadConfig()

	// 1. Initialize Valkey
	ctx, cancelInit := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelInit()

	rdb, err := config.InitValkey(ctx, cfg.ValkeyAddr)
	if err != nil {
		log.Fatalf("Valkey Init Error: %v", err)
	}
	defer rdb.Close()

	producer := queue.NewProducer(rdb, "ot_events_stream")

	// 2. Initialize PostgreSQL
	dbpool, err := config.InitDatabase(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database Init Error: %v", err)
	}
	defer dbpool.Close()

	// 3. Initialize Handlers
	ingestHandler := api.NewIngestHandler(producer)
	readHandler := api.NewReadHandler(dbpool)

	mux := http.NewServeMux()
	mux.Handle("/v1/ingest", ingestHandler)
	mux.HandleFunc("/v1/events", readHandler.ServeEvents)
	mux.HandleFunc("/v1/alerts", readHandler.ServeAlerts)
	
	// Add simple health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    ":8081",
		Handler: mux,
	}

	// 4. Graceful Shutdown
	go func() {
		log.Printf("Server listening on %s\n", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	// Finish in-flight requests
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	rdb.Close()
	log.Println("Server exiting")
}
