package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrations(t *testing.T) {
	pgURL := os.Getenv("DATABASE_URL")
	if pgURL == "" {
		t.Fatalf("DATABASE_URL not set, migration test requires a real database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Failed to ping database: %v", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Failed to acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("test_mig_%d", time.Now().UnixNano())
	
	// Safe identifier by quoting
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %q", schema)); err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}
	defer func() {
		conn.Exec(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %q CASCADE", schema))
	}()

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET search_path TO %q", schema)); err != nil {
		t.Fatalf("Failed to set search_path: %v", err)
	}

	// Run migrations
	files, err := os.ReadDir("migrations")
	if err != nil {
		files, err = os.ReadDir("server/migrations")
		if err != nil {
			t.Fatalf("Failed to read migrations directory: %v", err)
		}
	}

	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".sql") {
			path := filepath.Join("migrations", f.Name())
			if _, err := os.Stat(path); os.IsNotExist(err) {
				path = filepath.Join("server/migrations", f.Name())
			}

			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("Failed to read migration %s: %v", f.Name(), err)
			}

			_, err = conn.Exec(ctx, string(content))
			if err != nil {
				t.Fatalf("Failed to execute migration %s: %v", f.Name(), err)
			}
			t.Logf("Successfully applied migration: %s", f.Name())
		}
	}

	// Verify events table
	var exists bool
	err = conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables 
			WHERE table_schema = $1 AND table_name = 'events'
		)
	`, schema).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("Events table not found after migrations: %v", err)
	}

	// Verify alerts table
	err = conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables 
			WHERE table_schema = $1 AND table_name = 'alerts'
		)
	`, schema).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("Alerts table not found after migrations: %v", err)
	}
}
