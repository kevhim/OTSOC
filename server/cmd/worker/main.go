package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"redcyberfox/server/internal/config"
	"redcyberfox/server/internal/db"
	"redcyberfox/server/internal/queue"
)

func main() {
	log.Println("Starting RedCyberFox Worker (Phase 1 Vertical Slice)")

	cfg := config.LoadConfig()

	// 1. Initialize Valkey
	ctxInit, cancelInit := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelInit()

	rdb, err := config.InitValkey(ctxInit, cfg.ValkeyAddr)
	if err != nil {
		log.Fatalf("Valkey Init Error: %v", err)
	}
	defer rdb.Close()

	// 2. Initialize PostgreSQL
	dbpool, err := config.InitDatabase(ctxInit, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database Init Error: %v", err)
	}
	defer dbpool.Close()

	repo := db.NewRepository(dbpool)

	// 3. Initialize Consumer
	hostname, _ := os.Hostname()
	consumer := queue.NewConsumer(rdb, repo, "ot_events_stream", "worker_group", hostname)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutdown signal received, stopping consumer gracefully...")
		cancel() // This tells the consumer loop to stop accepting new work and exit
	}()

	// 4. Start processing
	if err := consumer.Start(ctx); err != nil {
		log.Fatalf("Consumer encountered fatal error: %v", err)
	}

	// The context cancellation ensures that in-flight items will attempt to finish
	// rapidly if they obey context, and the consumer loop will exit safely without ACKing partially completed work.
	
	// Adding small sleep for pending database ops to wrap up cleanly if needed
	time.Sleep(1 * time.Second)
	log.Println("Worker exited gracefully.")
}
