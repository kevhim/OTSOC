package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"redcyberfox/server/internal/db"
	"redcyberfox/server/internal/queue"
)

func main() {
	log.Println("Starting RedCyberFox Worker (Phase 1 Vertical Slice)")

	// 1. Initialize Valkey
	valkeyAddr := os.Getenv("VALKEY_ADDR")
	if valkeyAddr == "" {
		valkeyAddr = "localhost:6379"
	}
	rdb := redis.NewClient(&redis.Options{
		Addr: valkeyAddr,
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Failed to connect to Valkey: %v", err)
	}
	defer rdb.Close()

	// 2. Initialize PostgreSQL
	pgURL := os.Getenv("DATABASE_URL")
	if pgURL == "" {
		pgURL = "postgres://root:development_password@localhost:5432/redcyberfox"
	}
	dbpool, err := pgxpool.New(context.Background(), pgURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
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
