package main

import (
	"context"
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
		t.Skipf("DATABASE_URL not set, skipping migration test")
	}

	// Wait up to 5 seconds for DB
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Failed to ping database: %v", err)
	}

	schema := "test_" + time.Now().Format("20060102150405")
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("Failed to create transient schema: %v", err)
	}
	defer func() {
		// Drop transient schema on exit
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("Warning: failed to drop transient schema %s: %v", schema, err)
		}
	}()

	// Set search path so migrations only affect the transient schema
	if _, err := pool.Exec(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatalf("Failed to set search path: %v", err)
	}

	// Run migrations (if not already applied)
	files, err := os.ReadDir("migrations")
	if err != nil {
		// Try relative to server directory if run from root
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

			_, err = pool.Exec(ctx, string(content))
			if err != nil {
				t.Fatalf("Failed to execute migration %s: %v", f.Name(), err)
			}
			t.Logf("Successfully applied/verified migration: %s", f.Name())
		}
	}

	// Verify tables and constraints exist
	var exists bool
	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'events')", schema).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("events table not found in schema %s", schema)
	}

	err = pool.QueryRow(ctx, "SELECT EXISTS (SELECT FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'alerts')", schema).Scan(&exists)
	if err != nil || !exists {
		t.Fatalf("alerts table not found in schema %s", schema)
	}
}
