package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func assertEventually(t *testing.T, check func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

func TestOfflineSync_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	// Phase 1: Create local DB, store events, but server is "down"
	store1 := storage.NewSQLiteStorage(dbPath, identityPath, 10*1024*1024)
	if err := store1.Init(context.Background()); err != nil {
		t.Fatalf("Init 1 failed: %v", err)
	}

	ev1 := &events.CanonicalEvent{
		TenantID: "tenant-1",
		Severity: "INFO",
		Source:   "offline-test",
		Category: "syslog",
	}
	if err := store1.Store(context.Background(), ev1); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// 1. event is stored locally
	var count int
	if err := store1.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 pending event initially")
	}

	// 2. API unavailable
	srvFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503
	}))

	fwd1 := forwarder.NewForwarder(srvFail.URL, "tenant-1", store1)
	ctx1, cancel1 := context.WithCancel(context.Background())
	fwd1.Start(ctx1)
	fwd1.Wakeup()

	// Wait until attempt count increases (signaling a failed attempt)
	assertEventually(t, func() bool {
		var ac int
		err := store1.GetDB().QueryRow("SELECT attempt_count FROM events WHERE event_id = ?", ev1.EventID).Scan(&ac)
		return err == nil && ac > 0
	}, 2*time.Second)

	// 4. storage/forwarder is closed
	srvFail.Close()
	cancel1()

	// Wait for goroutine to exit via Stop
	fwd1.Stop()

	// 3. event remains pending (but might be skipped by GetPendingEvents due to backoff)
	if err := store1.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 pending event, got %d", count)
	}

	store1.Close() // Simulate Agent process crash/restart

	// 3. SQLite is reopened
	var capturedEvent events.CanonicalEvent
	srvUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}
		if err := json.Unmarshal(body, &capturedEvent); err != nil {
			t.Errorf("failed to unmarshal: %v", err)
		}
		w.WriteHeader(http.StatusAccepted) // 202
	}))
	defer srvUp.Close()

	store2 := storage.NewSQLiteStorage(dbPath, identityPath, 10*1024*1024)
	if err := store2.Init(context.Background()); err != nil {
		t.Fatalf("Init 2 failed: %v", err)
	}
	defer store2.Close()

	// 4. event still exists
	if err := store2.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("Expected 1 pending event on reopen, got %d", count)
	}

	// reset next_attempt_at so it processes immediately
	if _, err := store2.GetDB().Exec("UPDATE events SET next_attempt_at = NULL"); err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	// 5. forwarder restarts
	fwd2 := forwarder.NewForwarder(srvUp.URL, "tenant-1", store2)
	ctx2, cancel2 := context.WithCancel(context.Background())
	fwd2.Start(ctx2)
	fwd2.Wakeup()

	// 6. event is removed only after HTTP 202
	assertEventually(t, func() bool {
		var c int
		err := store2.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ?", ev1.EventID).Scan(&c)
		return err == nil && c == 0
	}, 2*time.Second)

	cancel2()
	fwd2.Stop()

	// 7. same event_id and seq_no is forwarded
	if capturedEvent.EventID != ev1.EventID {
		t.Errorf("Expected event ID %s, got %s", ev1.EventID, capturedEvent.EventID)
	}
	if capturedEvent.SeqNo != 1 {
		t.Errorf("Expected seq no 1, got %d", capturedEvent.SeqNo)
	}
}

func TestLostResponse_Integration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	store := storage.NewSQLiteStorage(dbPath, "", 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer store.Close()

	ev := &events.CanonicalEvent{
		TenantID: "tenant-1",
		Severity: "INFO",
		Source:   "lost-response-test",
	}
	if err := store.Store(context.Background(), ev); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	requestCount := 0
	logicalAcceptCount := 0
	acceptedEvents := make(map[string]bool)
	var capturedID string

	// Server that intentionally drops connection on first request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}
		var inc events.CanonicalEvent
		if err := json.Unmarshal(body, &inc); err != nil {
			t.Errorf("failed to unmarshal: %v", err)
		}

		if !acceptedEvents[inc.EventID] {
			acceptedEvents[inc.EventID] = true
			logicalAcceptCount++
		}
		capturedID = inc.EventID

		if requestCount == 1 {
			// Simulate connection dropped before 202 is sent
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					conn.Close()
					return
				}
			}
			// fallback if hijacker not available
			panic("connection dropped")
		}

		// Second request: server recognizes it as already accepted (idempotent accept)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	fwd := forwarder.NewForwarder(srv.URL, "tenant-1", store)
	ctx, cancel := context.WithCancel(context.Background())
	fwd.Start(ctx)

	// First wakeup triggers the first request
	fwd.Wakeup()
	assertEventually(t, func() bool {
		return requestCount == 1
	}, 2*time.Second)

	// Since we got 502, it should be marked as failed and retry backoff set
	// Reset backoff to trigger retry immediately
	if _, err := store.GetDB().Exec("UPDATE events SET next_attempt_at = NULL"); err != nil {
		t.Fatalf("Exec failed: %v", err)
	}

	// Trigger second request
	fwd.Wakeup()
	assertEventually(t, func() bool {
		return requestCount == 2
	}, 2*time.Second)

	// Event should be removed after the second request gets 202
	assertEventually(t, func() bool {
		var c int
		err := store.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ?", ev.EventID).Scan(&c)
		return err == nil && c == 0
	}, 2*time.Second)

	cancel()
	fwd.Stop()

	if requestCount != 2 {
		t.Errorf("Expected exactly 2 HTTP requests, got %d", requestCount)
	}
	if logicalAcceptCount != 1 {
		t.Errorf("Expected exactly 1 logical acceptance, got %d", logicalAcceptCount)
	}

	if capturedID != ev.EventID {
		t.Errorf("EventID changed or mismatch, expected %s got %s", ev.EventID, capturedID)
	}
}

func TestIntegration_LocalRemovalFailure(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	store := storage.NewSQLiteStorage(dbPath, "", 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	ev := &events.CanonicalEvent{
		TenantID: "tenant-1",
		Severity: "INFO",
		Source:   "removal-failure-test",
	}
	store.Store(context.Background(), ev)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	// Start forwarder
	fwd := forwarder.NewForwarder(srv.URL, "tenant-1", store)
	ctx, cancel := context.WithCancel(context.Background())
	fwd.Start(ctx)

	// Close the storage explicitly BEFORE waking up the forwarder.
	// This ensures that when the forwarder receives 202 and calls RemoveEvent,
	// RemoveEvent fails because the DB is closed ("DB not initialized").
	store.Close()

	fwd.Wakeup()
	fwd.Stop()
	cancel()

	// Re-open storage and verify event is still logically pending, and not in DLQ
	store2 := storage.NewSQLiteStorage(dbPath, "", 10*1024*1024)
	if err := store2.Init(context.Background()); err != nil {
		t.Fatalf("Init 2 failed: %v", err)
	}
	defer store2.Close()

	var count int
	if err := store2.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ? AND state = 'PENDING'", ev.EventID).Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 pending event, got %d", count)
	}

	if err := store2.GetDB().QueryRow("SELECT COUNT(*) FROM dead_letter_queue WHERE event_id = ?", ev.EventID).Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 DLQ events, got %d", count)
	}
}
