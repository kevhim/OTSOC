package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"redcyberfox/server/internal/api"
	"redcyberfox/server/internal/queue"
)

func main() {
	log.Println("Starting RedCyberFox API Server (Phase 1 Vertical Slice)")

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

	producer := queue.NewProducer(rdb, "ot_events_stream")

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
