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
}
