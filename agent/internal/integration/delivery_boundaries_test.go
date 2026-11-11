package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
			if err := os.WriteFile(barrierPath, []byte("ready\n"), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "failed to write barrier file %s: %v\n", barrierPath, err)
				os.Exit(5)
			}
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

	case "noisy_child":
		signalBarrier()
		// Rapidly emit copious output to stdout and stderr until killed
		chunk := strings.Repeat("X_NOISY_SUBPROCESS_STREAM_DATA_LINE_OT_SOC_1234567890\n", 64)
		for {
			_, _ = os.Stdout.WriteString(chunk)
			_, _ = os.Stderr.WriteString(chunk)
			time.Sleep(1 * time.Millisecond)
		}

	default:
		os.Exit(99)
	}
}

// CrashStreamLimitBytes defines the default per-stream output limit for crash subprocesses (64 KB).
const CrashStreamLimitBytes = 64 * 1024

// ManagedCrashProcess governs an external crash-test child process with thread-safe bounded buffers,
// explicit timeout contexts, guaranteed single reap via sync.Once, and distinct termination vs cleanup tracking.
type ManagedCrashProcess struct {
	cmd        *exec.Cmd
	stdoutBuf  *boundedBuffer
	stderrBuf  *boundedBuffer
	reapOnce   sync.Once
	reapErr    error
	cancelCtx  context.CancelFunc
	forcedKill bool
	exitCode   int
}

func (p *ManagedCrashProcess) Stdout() string {
	if p.stdoutBuf == nil {
		return ""
	}
	return p.stdoutBuf.String()
}

func (p *ManagedCrashProcess) Stderr() string {
	if p.stderrBuf == nil {
		return ""
	}
	return p.stderrBuf.String()
}

func (p *ManagedCrashProcess) StdoutTruncated() bool {
	if p.stdoutBuf == nil {
		return false
	}
	return p.stdoutBuf.IsTruncated()
}

func (p *ManagedCrashProcess) StderrTruncated() bool {
	if p.stderrBuf == nil {
		return false
	}
	return p.stderrBuf.IsTruncated()
}

func (p *ManagedCrashProcess) Truncated() bool {
	return p.StdoutTruncated() || p.StderrTruncated()
}

func (p *ManagedCrashProcess) ExitCode() int {
	return p.exitCode
}

func (p *ManagedCrashProcess) Diagnostics() string {
	return fmt.Sprintf("exitCode=%d, forcedKill=%v, reapErr=%v, stdoutLen=%d (trunc=%v), stderrLen=%d (trunc=%v)\n--- STDOUT ---\n%s\n--- STDERR ---\n%s",
		p.exitCode, p.forcedKill, p.reapErr, len(p.Stdout()), p.StdoutTruncated(), len(p.Stderr()), p.StderrTruncated(), p.Stdout(), p.Stderr())
}

// KillAndReap terminates the child process and reaps its exit state exactly once within a bounded 5s deadline.
// Forced termination via Process.Kill is distinguished from cleanup failure (such as reap timeout).
func (p *ManagedCrashProcess) KillAndReap() error {
	p.reapOnce.Do(func() {
		defer p.cancelCtx()
		p.forcedKill = true
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		done := make(chan error, 1)
		go func() {
			done <- p.cmd.Wait()
		}()
		select {
		case err := <-done:
			p.reapErr = err
			if p.cmd.ProcessState != nil {
				p.exitCode = p.cmd.ProcessState.ExitCode()
			}
		case <-time.After(5 * time.Second):
			p.reapErr = errors.New("timeout waiting for crash subprocess reap")
		}
	})
	return p.reapErr
}

func startCrashHelperSubprocessWithLimit(t *testing.T, mode, dbPath, idPath, barrierPath, serverURL string, streamLimit int) *ManagedCrashProcess {
	t.Helper()
	if streamLimit <= 0 {
		streamLimit = CrashStreamLimitBytes
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDeliveryCrashHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(),
		"GO_WANT_DELIVERY_CRASH_HELPER=1",
		"DELIVERY_CRASH_MODE="+mode,
		"DELIVERY_CRASH_DB_PATH="+dbPath,
		"DELIVERY_CRASH_ID_PATH="+idPath,
		"DELIVERY_CRASH_BARRIER_PATH="+barrierPath,
		"DELIVERY_CRASH_SERVER_URL="+serverURL,
	)

	stdoutBuf := newBoundedBuffer(streamLimit)
	stderrBuf := newBoundedBuffer(streamLimit)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("failed to start crash helper subprocess: %v", err)
	}

	p := &ManagedCrashProcess{
		cmd:       cmd,
		stdoutBuf: stdoutBuf,
		stderrBuf: stderrBuf,
		cancelCtx: cancel,
	}

	// Register cleanup immediately after successful Start so child is always reaped
	t.Cleanup(func() {
		if err := p.KillAndReap(); err != nil {
			if strings.Contains(err.Error(), "timeout waiting") {
				t.Errorf("subprocess cleanup failure: wait timed out: %v (stdout_trunc=%v, stderr_trunc=%v)",
					err, p.StdoutTruncated(), p.StderrTruncated())
			}
			// Note: an ExitError after Process.Kill is expected forced termination, not a cleanup failure.
		}
	})

	return p
}

func startCrashHelperSubprocess(t *testing.T, mode, dbPath, idPath, barrierPath, serverURL string) *ManagedCrashProcess {
	t.Helper()
	return startCrashHelperSubprocessWithLimit(t, mode, dbPath, idPath, barrierPath, serverURL, CrashStreamLimitBytes)
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

	p := startCrashHelperSubprocess(t, "storage_commit_and_hang", dbPath, idPath, barrierPath, "")
	waitForFileBarrier(t, barrierPath, 5*time.Second)

	// Abruptly terminate the subprocess and reap cleanly
	_ = p.KillAndReap()

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

	rows, err := store.GetDB().Query("SELECT event_id, seq_no, state FROM events ORDER BY seq_no ASC")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	defer rows.Close()

	type recoveredEvent struct {
		id    string
		seqNo int64
		state string
	}
	var recovered []recoveredEvent
	for rows.Next() {
		var r recoveredEvent
		if err := rows.Scan(&r.id, &r.seqNo, &r.state); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		recovered = append(recovered, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration error: %v", err)
	}

	expected := []recoveredEvent{
		{id: "crash-event-001", seqNo: 1, state: "PENDING"},
		{id: "crash-event-002", seqNo: 2, state: "PENDING"},
		{id: "crash-event-003", seqNo: 3, state: "PENDING"},
	}
	if len(recovered) != len(expected) {
		t.Fatalf("expected %d events recovered, found %d: %+v", len(expected), len(recovered), recovered)
	}
	for i, exp := range expected {
		if recovered[i] != exp {
			t.Errorf("mismatch at index %d: expected %+v, got %+v", i, exp, recovered[i])
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

	p := startCrashHelperSubprocess(t, "storage_uncommitted_delete_and_hang", dbPath, idPath, barrierPath, "")
	waitForFileBarrier(t, barrierPath, 5*time.Second)

	// Kill while transaction is open and uncommitted
	_ = p.KillAndReap()

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

	var id, state string
	var seqNo int64
	err := store.GetDB().QueryRow("SELECT event_id, seq_no, state FROM events WHERE event_id = 'uncommitted-delete-event-001'").Scan(&id, &seqNo, &state)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if id != "uncommitted-delete-event-001" || seqNo != 1 || state != "PENDING" {
		t.Fatalf("expected uncommitted delete to roll back to id='uncommitted-delete-event-001', seqNo=1, state='PENDING'; got id=%s, seqNo=%d, state=%s", id, seqNo, state)
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

	p := startCrashHelperSubprocess(t, "forwarder_in_flight_crash", dbPath, idPath, barrierPath, srv.URL)
	waitForFileBarrier(t, barrierPath, 5*time.Second)

	// Wait until server receives the in-flight forwarder request
	select {
	case <-serverHit:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for forwarder to reach test server")
	}

	// Terminate child while request is in-flight before HTTP 202 response or deletion
	_ = p.KillAndReap()

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

	var id, state string
	var seqNo int64
	err := store.GetDB().QueryRow("SELECT event_id, seq_no, state FROM events WHERE event_id = 'forwarder-inflight-event-001'").Scan(&id, &seqNo, &state)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if id != "forwarder-inflight-event-001" || seqNo != 1 || state != "PENDING" {
		t.Fatalf("expected in-flight event to remain in SQLite with id='forwarder-inflight-event-001', seqNo=1, state='PENDING'; got id=%s, seqNo=%d, state=%s", id, seqNo, state)
	}
}

// 11. Noisy Child Process:
// Verifies that a runaway child process producing high-volume output does not cause unbounded
// buffer growth, that buffers truncate at the configured boundary, truncation is explicitly exposed,
// and the process is killed and reaped within the bounded deadline with zero leaks.
func TestCrashBoundary_NoisyChildProcess_BoundedOutputAndReap(t *testing.T) {
	tmpDir := t.TempDir()
	barrierPath := filepath.Join(tmpDir, "barrier.ready")

	limit := 32 * 1024 // 32 KB per stream
	p := startCrashHelperSubprocessWithLimit(t, "noisy_child", "", "", barrierPath, "", limit)
	waitForFileBarrier(t, barrierPath, 5*time.Second)

	// Allow child to produce at least a couple hundred KB of output
	time.Sleep(100 * time.Millisecond)

	// Terminate child and reap
	killErr := p.KillAndReap()

	// 1. Output must be bounded to approximately limit + truncation notice
	stdoutLen := len(p.Stdout())
	stderrLen := len(p.Stderr())

	if stdoutLen > limit+512 {
		t.Errorf("stdout exceeded bounded limit: %d bytes (limit was %d)", stdoutLen, limit)
	}
	if stderrLen > limit+512 {
		t.Errorf("stderr exceeded bounded limit: %d bytes (limit was %d)", stderrLen, limit)
	}

	// 2. Truncation flags must be true
	if !p.StdoutTruncated() {
		t.Errorf("expected stdout to be marked truncated")
	}
	if !p.StderrTruncated() {
		t.Errorf("expected stderr to be marked truncated")
	}
	if !p.Truncated() {
		t.Errorf("expected Truncated() to report true")
	}

	// 3. Output must contain the explicit truncation notice
	if !strings.Contains(p.Stdout(), "[OUTPUT TRUNCATED") {
		t.Errorf("stdout missing truncation marker")
	}
	if !strings.Contains(p.Stderr(), "[OUTPUT TRUNCATED") {
		t.Errorf("stderr missing truncation marker")
	}

	// 4. Reap must complete within 5s deadline (killErr must not be reap timeout)
	if killErr != nil && strings.Contains(killErr.Error(), "timeout waiting") {
		t.Fatalf("reap timed out: %v", killErr)
	}

	// 5. Diagnostics must report truncation and state correctly
	diag := p.Diagnostics()
	if !strings.Contains(diag, "trunc=true") {
		t.Errorf("Diagnostics() did not record truncation status: %s", diag)
	}
}
