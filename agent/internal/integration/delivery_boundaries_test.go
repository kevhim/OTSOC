package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"redcyberfox/agent/internal/forwarder"
	"redcyberfox/agent/internal/governor"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

// Helper to poll until condition or fail after timeout
func waitForCondition(t *testing.T, check func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for condition: %s", msg)
}

// 1. Receiver Unavailable: verifies offline backoff, state transition, and zero event loss
func TestDeliveryBoundary_ReceiverUnavailable(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}
	defer store.Close()

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      "tenant-boundary",
		Severity:      "WARN",
		Source:        "boundary_test",
		Category:      "network_event",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
	}
	if err := store.Store(context.Background(), ev); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	// Server that is closed to simulate connection refused (transport unavailable)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := srv.URL
	srv.Close() // Close immediately so all TCP connects fail

	fwd := forwarder.NewForwarder(serverURL, "tenant-boundary", store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	fwd.Wakeup()

	waitForCondition(t, func() bool {
		return fwd.NetworkState() == governor.NetworkOffline
	}, 2*time.Second, "network state offline on transport failure")

	// Verify SQLite invariant: event is retained PENDING, attempt_count > 0, next_attempt_at in future
	var state string
	var attempts int
	var nextAttempt *time.Time
	err := store.GetDB().QueryRow("SELECT state, attempt_count, next_attempt_at FROM events WHERE event_id = ?", ev.EventID).Scan(&state, &attempts, &nextAttempt)
	if err != nil {
		t.Fatalf("failed to query event: %v", err)
	}
	if state != "PENDING" {
		t.Errorf("expected state PENDING, got %s", state)
	}
	if attempts == 0 {
		t.Errorf("expected attempts > 0, got %d", attempts)
	}
	if nextAttempt == nil {
		t.Errorf("expected next_attempt_at to be populated")
	}
}

// 2. Receiver Reconnecting: verifies transition from offline to online and successful drain
func TestDeliveryBoundary_ReceiverReconnecting(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}
	defer store.Close()

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      "tenant-boundary",
		Severity:      "INFO",
		Source:        "boundary_test",
		Category:      "system_metric",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
	}
	if err := store.Store(context.Background(), ev); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	var isOnline atomic.Bool
	isOnline.Store(false)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isOnline.Load() {
			// Simulate abrupt connection drop at transport layer
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					conn.Close()
					return
				}
			}
			http.Error(w, "dropped", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted) // 202
	}))
	defer srv.Close()

	fwd := forwarder.NewForwarder(srv.URL, "tenant-boundary", store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	// Initial failing attempt (connection dropped -> networkOffline)
	fwd.Wakeup()
	waitForCondition(t, func() bool {
		return fwd.NetworkState() == governor.NetworkOffline
	}, 2*time.Second, "network state offline after failure")

	// Server reconnects/heals
	isOnline.Store(true)

	// Advance storage clock past backoff
	store.SetClock(func() time.Time {
		return time.Now().UTC().Add(10 * time.Minute)
	})

	fwd.Wakeup()

	waitForCondition(t, func() bool {
		return fwd.NetworkState() == governor.NetworkOnline
	}, 2*time.Second, "network state online after reconnection")

	// Verify event was cleanly removed
	waitForCondition(t, func() bool {
		var c int
		err := store.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ?", ev.EventID).Scan(&c)
		return err == nil && c == 0
	}, 2*time.Second, "event removed locally upon reconnect delivery")
}

// 3. Timeout During Upload: verifies partial write/transport failure handling
func TestDeliveryBoundary_TimeoutDuringUpload(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}
	defer store.Close()

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      "tenant-boundary",
		Severity:      "INFO",
		Source:        "timeout_test",
		Category:      "audit",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
	}
	if err := store.Store(context.Background(), ev); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	// Server sleeps longer than forwarder timeout
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	fwd := forwarder.NewForwarder(srv.URL, "tenant-boundary", store)
	// Set very short timeout on forwarder
	fwd.SetHTTPTimeout(15 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	fwd.Wakeup()

	waitForCondition(t, func() bool {
		return fwd.NetworkState() == governor.NetworkOffline
	}, 2*time.Second, "network state offline after timeout")

	// Event MUST NOT be removed on timeout
	var count int
	if err := store.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ? AND state = 'PENDING'", ev.EventID).Scan(&count); err != nil {
		t.Fatalf("failed to query event: %v", err)
	}
	if count != 1 {
		t.Errorf("expected event to remain in PENDING state after upload timeout")
	}
}

// 4. Duplicate ACK / Retry: verifies idempotency across lost acknowledgements
func TestDeliveryBoundary_DuplicateAckAndRetry(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}
	defer store.Close()

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      "tenant-boundary",
		Severity:      "HIGH",
		Source:        "dup_ack_test",
		Category:      "security_alert",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
	}
	if err := store.Store(context.Background(), ev); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	var attempts atomic.Int32
	var acceptedIDs sync.Map

	// Server receives event, records it, but aborts connection on attempt 1 before response reaches agent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)
		body, _ := io.ReadAll(r.Body)
		var inc events.CanonicalEvent
		_ = json.Unmarshal(body, &inc)
		acceptedIDs.Store(inc.EventID, true)

		if att == 1 {
			// Abruptly close connection without sending status
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					conn.Close()
					return
				}
			}
			http.Error(w, "dropped", http.StatusInternalServerError)
			return
		}

		// Subsequent attempt: acknowledge idempotently
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	fwd := forwarder.NewForwarder(srv.URL, "tenant-boundary", store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	// First attempt drops
	fwd.Wakeup()
	waitForCondition(t, func() bool {
		return attempts.Load() >= 1
	}, 2*time.Second, "attempt 1 completed")

	// Advance clock past retry backoff
	store.SetClock(func() time.Time {
		return time.Now().UTC().Add(10 * time.Minute)
	})

	// Retry
	fwd.Wakeup()
	waitForCondition(t, func() bool {
		return attempts.Load() >= 2
	}, 2*time.Second, "attempt 2 completed")

	// Verify local removal after duplicate acceptance
	waitForCondition(t, func() bool {
		var c int
		err := store.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ?", ev.EventID).Scan(&c)
		return err == nil && c == 0
	}, 2*time.Second, "event removed locally after retry acceptance")

	if attempts.Load() != 2 {
		t.Errorf("expected exactly 2 HTTP requests, got %d", attempts.Load())
	}
	if _, ok := acceptedIDs.Load(ev.EventID); !ok {
		t.Errorf("expected server to record event ID %s", ev.EventID)
	}
}

// 5. Clean Restart With Pending SQLite Events (Graceful Reopen Coverage)
// Verifies that when the agent is shut down cleanly while events are pending in SQLite,
// reopening the database preserves event IDs, sequential numbers, and resumes delivery.
// (For unannounced process termination crash coverage, see TestCrashBoundary_SubprocessStorageCrash).
func TestDeliveryBoundary_GracefulRestartWithPendingSQLiteEvents(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	// Step 1: Pre-restart execution: populate 5 pending events
	store1 := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store1.Init(context.Background()); err != nil {
		t.Fatalf("store1 init failed: %v", err)
	}

	eventIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		eventIDs[i] = uuid.New().String()
		ev := &events.CanonicalEvent{
			EventID:       eventIDs[i],
			TenantID:      "tenant-restart",
			Severity:      "INFO",
			Source:        "restart_test",
			Category:      "telemetry",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
		}
		if err := store1.Store(context.Background(), ev); err != nil {
			t.Fatalf("store1 store event %d failed: %v", i, err)
		}
	}

	// Gracefully close store1 without forwarding (clean shutdown simulation)
	if err := store1.Close(); err != nil {
		t.Fatalf("store1 close failed: %v", err)
	}

	// Step 2: Post-restart execution: reopen storage independently
	store2 := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store2.Init(context.Background()); err != nil {
		t.Fatalf("store2 init failed: %v", err)
	}
	defer store2.Close()

	// Verify all 5 events exist with state PENDING and preserved seq numbers
	pending, err := store2.GetPendingEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("store2 get pending failed: %v", err)
	}
	if len(pending) != 5 {
		t.Fatalf("expected 5 pending events on restart, found %d", len(pending))
	}
	for i, ev := range pending {
		if ev.SeqNo != int64(i+1) {
			t.Errorf("event %d: expected seq_no %d, got %d", i, i+1, ev.SeqNo)
		}
	}

	// Step 3: Forward recovered events to server
	deliveredCount := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deliveredCount.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	fwd := forwarder.NewForwarder(srv.URL, "tenant-restart", store2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	fwd.Wakeup()

	waitForCondition(t, func() bool {
		return deliveredCount.Load() == 5
	}, 3*time.Second, "all 5 restarted events delivered")

	waitForCondition(t, func() bool {
		var count int
		err := store2.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE state = 'PENDING'").Scan(&count)
		return err == nil && count == 0
	}, 2*time.Second, "all restarted events removed locally after delivery")
}

// 6. Storage Transaction Rollback Durability (Storage-Level Rollback Coverage)
// Verifies that an explicit database transaction rollback restores the uncommitted state.
// (For abrupt process termination during forwarding, see TestCrashBoundary_SubprocessUncommittedDelete).
func TestDeliveryBoundary_StorageTransactionRollbackDurability(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      "tenant-crash",
		Severity:      "INFO",
		Source:        "crash_test",
		Category:      "checkpoint",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
	}
	if err := store.Store(context.Background(), ev); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	// Simulate an in-flight deletion transaction that is rolled back (process dies before commit)
	tx, err := store.GetDB().Begin()
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	_, err = tx.Exec("DELETE FROM events WHERE event_id = ?", ev.EventID)
	if err != nil {
		t.Fatalf("failed to exec delete: %v", err)
	}
	// Abrupt abort / rollback without commit
	if err := tx.Rollback(); err != nil {
		t.Fatalf("failed to rollback tx: %v", err)
	}
	store.Close()

	// Reopen storage independently
	storeRecovered := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := storeRecovered.Init(context.Background()); err != nil {
		t.Fatalf("failed to reopen storage: %v", err)
	}
	defer storeRecovered.Close()

	var count int
	if err := storeRecovered.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ? AND state = 'PENDING'", ev.EventID).Scan(&count); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected event to remain intact after aborted removal transaction, found %d", count)
	}
}

// 7. ACK Without Durable Receiver Persistence: verifies the exact boundary between edge and central durability
func TestDeliveryBoundary_AckWithoutDurableReceiverPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")

	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to init storage: %v", err)
	}
	defer store.Close()

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      "tenant-boundary",
		Severity:      "CRITICAL",
		Source:        "ack_boundary_test",
		Category:      "security_alert",
		OccurredAt:    time.Now().UTC(),
		SchemaVersion: events.CurrentSchemaVersion,
	}
	if err := store.Store(context.Background(), ev); err != nil {
		t.Fatalf("store failed: %v", err)
	}

	// Server simulates receiving the HTTP POST and returning HTTP 202 Accepted,
	// but intentionally drops the event from memory without storing to central DB.
	var serverReceived atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverReceived.Store(true)
		// Upstream accepts HTTP transport
		w.WriteHeader(http.StatusAccepted)
		// Intentionally does NOT persist centrally
	}))
	defer srv.Close()

	fwd := forwarder.NewForwarder(srv.URL, "tenant-boundary", store)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := fwd.Start(ctx); err != nil {
		t.Fatalf("failed to start forwarder: %v", err)
	}
	defer fwd.Stop()

	fwd.Wakeup()

	waitForCondition(t, func() bool {
		return serverReceived.Load()
	}, 2*time.Second, "server received request")

	// The Edge Agent contract: Local removal is triggered strictly by HTTP 202 response.
	waitForCondition(t, func() bool {
		var c int
		err := store.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = ?", ev.EventID).Scan(&c)
		return err == nil && c == 0
	}, 2*time.Second, "local event removed upon HTTP 202")

	// Verify forwarder accounting:
	acc := fwd.Accounting()
	if acc.HTTPAcceptedTotal != 1 {
		t.Errorf("expected HTTPAcceptedTotal = 1, got %d", acc.HTTPAcceptedTotal)
	}
	if acc.LocalRemovedTotal != 1 {
		t.Errorf("expected LocalRemovedTotal = 1, got %d", acc.LocalRemovedTotal)
	}

	// Contract clarification: The edge agent guarantees delivery to the HTTP ingress boundary.
	// Durable ingestion on the central cluster is the responsibility of the central platform after HTTP 202.
}

// =============================================================================
// Truthful Subprocess Crash Regressions (Process Termination via Process.Kill)
// =============================================================================

// TestDeliveryCrashHelperProcess is not run as a standalone test; it is invoked as a subprocess
// by TestCrashBoundary_* tests with GO_WANT_DELIVERY_CRASH_HELPER=1 to test abrupt process death.
func TestDeliveryCrashHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DELIVERY_CRASH_HELPER") != "1" {
		return
	}

	mode := os.Getenv("DELIVERY_CRASH_MODE")
	dbPath := os.Getenv("DELIVERY_CRASH_DB_PATH")
	idPath := os.Getenv("DELIVERY_CRASH_ID_PATH")
	barrierPath := os.Getenv("DELIVERY_CRASH_BARRIER_PATH")
	serverURL := os.Getenv("DELIVERY_CRASH_SERVER_URL")

	signalBarrier := func() {
		if barrierPath != "" {
			_ = os.WriteFile(barrierPath, []byte("ready\n"), 0644)
		}
	}

	switch mode {
	case "storage_commit_and_hang":
		store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
		if err := store.Init(context.Background()); err != nil {
			os.Exit(1)
		}
		// Insert 3 known events
		for i := 1; i <= 3; i++ {
			ev := &events.CanonicalEvent{
				EventID:       fmt.Sprintf("crash-event-%03d", i),
				TenantID:      "tenant-crash-proc",
				Severity:      "INFO",
				Source:        "crash_subproc",
				Category:      "checkpoint",
				OccurredAt:    time.Now().UTC(),
				SchemaVersion: events.CurrentSchemaVersion,
			}
			if err := store.Store(context.Background(), ev); err != nil {
				os.Exit(2)
			}
		}
		// Signal to parent that data is committed to SQLite
		signalBarrier()
		// Hang indefinitely; parent will kill this process with TerminateProcess / SIGKILL
		select {}

	case "storage_uncommitted_delete_and_hang":
		store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
		if err := store.Init(context.Background()); err != nil {
			os.Exit(1)
		}
		ev := &events.CanonicalEvent{
			EventID:       "uncommitted-delete-event-001",
			TenantID:      "tenant-crash-proc",
			Severity:      "WARN",
			Source:        "crash_subproc",
			Category:      "checkpoint",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
		}
		if err := store.Store(context.Background(), ev); err != nil {
			os.Exit(2)
		}

		// Begin a transaction and delete the event without committing
		tx, err := store.GetDB().Begin()
		if err != nil {
			os.Exit(3)
		}
		if _, err := tx.Exec("DELETE FROM events WHERE event_id = ?", ev.EventID); err != nil {
			os.Exit(4)
		}
		// Deliberately hold tx open and do NOT commit
		signalBarrier()
		// Hang indefinitely with open uncommitted transaction until killed
		select {}

	case "forwarder_in_flight_crash":
		store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
		if err := store.Init(context.Background()); err != nil {
			os.Exit(1)
		}
		ev := &events.CanonicalEvent{
			EventID:       "forwarder-inflight-event-001",
			TenantID:      "tenant-crash-proc",
			Severity:      "CRITICAL",
			Source:        "crash_subproc",
			Category:      "checkpoint",
			OccurredAt:    time.Now().UTC(),
			SchemaVersion: events.CurrentSchemaVersion,
		}
		if err := store.Store(context.Background(), ev); err != nil {
			os.Exit(2)
		}

		fwd := forwarder.NewForwarder(serverURL, "tenant-crash-proc", store)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := fwd.Start(ctx); err != nil {
			os.Exit(3)
		}
		defer fwd.Stop()
		fwd.Wakeup()

		// Signal parent that forwarder started and is attempting delivery
		signalBarrier()
		// Hang; the parent kills the child while HTTP request is in-flight
		select {}

	default:
		os.Exit(99)
	}
}

func startCrashHelperSubprocess(t *testing.T, mode, dbPath, idPath, barrierPath, serverURL string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeliveryCrashHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(),
		"GO_WANT_DELIVERY_CRASH_HELPER=1",
		"DELIVERY_CRASH_MODE="+mode,
		"DELIVERY_CRASH_DB_PATH="+dbPath,
		"DELIVERY_CRASH_ID_PATH="+idPath,
		"DELIVERY_CRASH_BARRIER_PATH="+barrierPath,
		"DELIVERY_CRASH_SERVER_URL="+serverURL,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start crash helper subprocess: %v", err)
	}
	return cmd
}

func waitForFileBarrier(t *testing.T, barrierPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(barrierPath); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for barrier file %s after %v", barrierPath, timeout)
}

// 8. Subprocess Storage Crash (Unannounced Process Termination):
// Verifies that when a process terminates abruptly (simulated via Process.Kill / TerminateProcess)
// after committing SQLite records, reopening the database recovers all committed events in PENDING state.
//
// Boundary Note: This tests OS-level unannounced process termination (SIGKILL / TerminateProcess).
// It does NOT establish physical power loss or hardware cache durability, which requires hardware-level write barriers.
func TestCrashBoundary_SubprocessStorageCrash_ProcessTermination(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")
	barrierPath := filepath.Join(tmpDir, "barrier.ready")

	cmd := startCrashHelperSubprocess(t, "storage_commit_and_hang", dbPath, idPath, barrierPath, "")
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	waitForFileBarrier(t, barrierPath, 5*time.Second)

	// Abruptly terminate the subprocess without clean shutdown / DB close
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill subprocess: %v", err)
	}
	_ = cmd.Wait()

	// Reopen storage independently
	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to initialize reopened storage: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("failed to close storage cleanly: %v", err)
		}
	}()

	rows, err := store.GetDB().Query("SELECT event_id, state FROM events ORDER BY seq_no ASC")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer rows.Close()

	var eventIDs []string
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		if state != "PENDING" {
			t.Errorf("expected state PENDING, got %s for event %s", state, id)
		}
		eventIDs = append(eventIDs, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration error: %v", err)
	}

	expectedIDs := []string{"crash-event-001", "crash-event-002", "crash-event-003"}
	if len(eventIDs) != len(expectedIDs) {
		t.Fatalf("expected %d events recovered, found %d: %v", len(expectedIDs), len(eventIDs), eventIDs)
	}
	for i, exp := range expectedIDs {
		if eventIDs[i] != exp {
			t.Errorf("mismatch at index %d: expected %s, got %s", i, exp, eventIDs[i])
		}
	}
}

// 9. Subprocess Uncommitted Delete Rollback (Crash During In-Flight Transaction):
// Verifies that when a process terminates abruptly while holding an uncommitted DELETE transaction,
// the uncommitted deletion is rolled back by SQLite recovery and the event remains intact.
func TestCrashBoundary_SubprocessUncommittedDelete_ProcessTermination(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")
	barrierPath := filepath.Join(tmpDir, "barrier.ready")

	cmd := startCrashHelperSubprocess(t, "storage_uncommitted_delete_and_hang", dbPath, idPath, barrierPath, "")
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	waitForFileBarrier(t, barrierPath, 5*time.Second)

	// Kill while transaction is open and uncommitted
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill subprocess: %v", err)
	}
	_ = cmd.Wait()

	// Reopen storage independently
	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to reopen storage after uncommitted crash: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("failed to close storage cleanly: %v", err)
		}
	}()

	var count int
	err := store.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = 'uncommitted-delete-event-001' AND state = 'PENDING'").Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected uncommitted delete to be rolled back (count=1), found count=%d", count)
	}
}

// 10. Subprocess Forwarder In-Flight Crash:
// Verifies that if the agent process terminates abruptly while a forwarder request is in-flight
// before HTTP 202 is received and deletion is committed, the event is NOT prematurely removed from storage.
func TestCrashBoundary_SubprocessForwarder_ProcessTermination(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "agent.db")
	idPath := filepath.Join(tmpDir, "identity.json")
	barrierPath := filepath.Join(tmpDir, "barrier.ready")

	serverHit := make(chan struct{})
	serverHold := make(chan struct{})
	var serverHitOnce sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHitOnce.Do(func() {
			close(serverHit)
		})
		// Hold the request open until parent signals or timeout
		select {
		case <-serverHold:
		case <-time.After(5 * time.Second):
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	defer func() {
		select {
		case <-serverHold:
		default:
			close(serverHold)
		}
	}()

	cmd := startCrashHelperSubprocess(t, "forwarder_in_flight_crash", dbPath, idPath, barrierPath, srv.URL)
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	waitForFileBarrier(t, barrierPath, 5*time.Second)

	// Wait until server receives the in-flight forwarder request
	select {
	case <-serverHit:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for forwarder to reach test server")
	}

	// Terminate child while request is in-flight before HTTP 202 response or deletion
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill forwarder subprocess: %v", err)
	}
	_ = cmd.Wait()

	// Reopen storage independently and verify event was NOT deleted prematurely
	store := storage.NewSQLiteStorage(dbPath, idPath, 10*1024*1024)
	if err := store.Init(context.Background()); err != nil {
		t.Fatalf("failed to reopen storage: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("failed to close storage cleanly: %v", err)
		}
	}()

	var count int
	err := store.GetDB().QueryRow("SELECT COUNT(*) FROM events WHERE event_id = 'forwarder-inflight-event-001' AND state = 'PENDING'").Scan(&count)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected in-flight event to remain in SQLite PENDING state, found %d", count)
	}
}
