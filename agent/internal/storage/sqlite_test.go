package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

func TestStorage_InitAndMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	// Write mock identity.json
	os.WriteFile(identityPath, []byte(`{"device_id":"migrated-id"}`), 0600)

	s := NewSQLiteStorage(dbPath, identityPath, 1024*1024*10)
	err := s.Init(context.Background())
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer s.Close()

	if s.GetDeviceID() != "migrated-id" {
		t.Errorf("Expected migrated-id, got %s", s.GetDeviceID())
	}

	// Ensure identity file deleted
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Errorf("identity file should be deleted")
	}
}

func TestStorage_StoreIdempotency(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", int64(1024*1024*10))
	s.Init(context.Background())
	defer s.Close()

	ev := &events.CanonicalEvent{
		EventID:  "idempotent-id-1",
		Severity: "INFO",
		Source:   "test1",
	}

	err := s.Store(context.Background(), ev)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if ev.EventID != "idempotent-id-1" {
		t.Errorf("EventID should be preserved")
	}
	if ev.SeqNo != 1 {
		t.Errorf("SeqNo should be 1, got %d", ev.SeqNo)
	}

	// Try inserting same event_id
	err = s.Store(context.Background(), ev)
	if err != nil {
		t.Fatalf("Idempotent store failed: %v", err)
	}

	// Verify seq_no didn't increment
	var nextSeq int
	if err := s.db.QueryRow("SELECT next_seq_no FROM agent_state WHERE id=1").Scan(&nextSeq); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if nextSeq != 2 {
		t.Errorf("SeqNo should not have incremented, is %d", nextSeq)
	}
}

func TestStorage_DiskQuotaEnforcement(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	// Small quota to trigger prune quickly
	s := NewSQLiteStorage(dbPath, "", int64(50000))
	s.Init(context.Background())
	defer s.Close()

	for i := 0; i < 100; i++ {
		ev := &events.CanonicalEvent{
			EventID:  fmt.Sprintf("debug-%d", i),
			Severity: "DEBUG",
			Source:   "large payload to trigger quota 0000000000000000000000000000000",
		}
		s.Store(context.Background(), ev)
	}

	critEv := &events.CanonicalEvent{
		EventID:  "critical-1",
		Severity: "CRITICAL",
		Source:   "critical event",
	}
	err := s.Store(context.Background(), critEv)
	if err != nil {
		t.Fatalf("Critical store failed: %v", err)
	}

	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM events WHERE severity = 'CRITICAL'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Critical event missing")
	}

	var debugCount int
	if err := s.db.QueryRow("SELECT count(*) FROM events WHERE severity = 'DEBUG'").Scan(&debugCount); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if debugCount == 100 {
		t.Errorf("Debug events should have been pruned")
	}
}

func TestStorage_CorruptDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create corrupt DB
	os.WriteFile(dbPath, []byte("NOT A SQLITE DB FILE"), 0600)

	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	err := s.Init(context.Background())
	if err == nil {
		t.Fatalf("Init should fail on corrupt DB")
	}

	if err.Error()[:16] != "DEGRADED_STORAGE" {
		t.Errorf("Expected DEGRADED_STORAGE error, got: %v", err)
	}

	matches, _ := filepath.Glob(dbPath + ".corrupt.*")
	if len(matches) == 0 {
		t.Errorf("Corrupt DB should be backed up")
	}
}

func TestStorage_DLQIdempotency(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	s.Init(context.Background())
	defer s.Close()

	ev := &events.CanonicalEvent{
		EventID:  "test-id",
		SeqNo:    1,
		Severity: "INFO",
	}
	s.Store(context.Background(), ev)

	err := s.MoveToDLQ(context.Background(), ev, "http_400", "bad request")
	if err != nil {
		t.Fatalf("MoveToDLQ failed: %v", err)
	}

	err = s.MoveToDLQ(context.Background(), ev, "http_400", "bad request")
	if err != nil {
		t.Fatalf("Idempotent MoveToDLQ failed: %v", err)
	}

	// Verify exactly 1 DLQ row
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM dead_letter_queue WHERE event_id = ?", ev.EventID).Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 DLQ row, got %d", count)
	}

	// Verify events table has no row
	if err := s.db.QueryRow("SELECT count(*) FROM events WHERE event_id = ?", ev.EventID).Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 events row, got %d", count)
	}

	// Try DLQ with different payload
	evDiff := &events.CanonicalEvent{
		EventID:  ev.EventID,
		SeqNo:    ev.SeqNo,
		Severity: "CRITICAL",
	}
	err = s.MoveToDLQ(context.Background(), evDiff, "http_400", "bad request")
	if err == nil {
		t.Fatalf("MoveToDLQ with different payload should fail with integrity error")
	}
}

func TestStorage_MaintenanceLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	err := s.Init(context.Background())
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	// Close should stop the maintenance goroutine cleanly
	err = s.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	// Give it a moment to panic if there was a WaitGroup issue
}

func TestStorage_CorruptPayloadDLQMove(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	s.Init(context.Background())
	defer s.Close()

	// Insert raw corrupt payload directly into events
	_, err := s.db.Exec(`
		INSERT INTO events (event_id, device_id, seq_no, severity, payload, state)
		VALUES ('corrupt-1', 'dev-1', 1, 'INFO', '{invalid-json', 'PENDING')
	`)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Calling GetPendingEvents should detect deserialization error and move it to DLQ
	_, err = s.GetPendingEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetPendingEvents failed: %v", err)
	}

	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM dead_letter_queue WHERE event_id = 'corrupt-1'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 DLQ row for corrupt payload, got %d", count)
	}

	if err := s.db.QueryRow("SELECT count(*) FROM events WHERE event_id = 'corrupt-1'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 events row after DLQ move, got %d", count)
	}
}

func TestStorage_CorruptPayloadDLQMoveError(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	s.Init(context.Background())
	defer s.Close()

	// Insert raw corrupt payload directly into events
	_, err := s.db.Exec(`
		INSERT INTO events (event_id, device_id, seq_no, severity, payload, state)
		VALUES ('corrupt-1', 'dev-1', 1, 'INFO', '{invalid-json', 'PENDING')
	`)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Drop DLQ table to force moveRawToDLQLocked to fail
	_, err = s.db.Exec(`DROP TABLE dead_letter_queue`)
	if err != nil {
		t.Fatalf("Drop failed: %v", err)
	}

	// Calling GetPendingEvents should fail and return error
	_, err = s.GetPendingEvents(context.Background(), 10)
	if err == nil {
		t.Fatalf("GetPendingEvents should fail when DLQ move fails")
	}

	// Event should remain in events table
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM events WHERE event_id = 'corrupt-1'").Scan(&count); err != nil {
		t.Fatalf("QueryRow failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 events row to remain, got %d", count)
	}
}

func TestStorage_LegacyIdentityCleanupFailureIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	// Write valid legacy identity
	os.WriteFile(identityPath, []byte(`{"device_id":"legacy-dev-1"}`), 0600)

	s1 := NewSQLiteStorage(dbPath, identityPath, 1024*1024*10)
	err := s1.Init(context.Background())
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Recreate the identity file to simulate failure to delete
	os.WriteFile(identityPath, []byte(`{"device_id":"legacy-dev-1"}`), 0600)

	// Close and recreate DB
	s1.Close()

	s2 := NewSQLiteStorage(dbPath, identityPath, 1024*1024*10)
	err = s2.Init(context.Background())
	if err != nil {
		t.Fatalf("Init 2 failed: %v", err)
	}
	defer s2.Close()

	if s2.GetDeviceID() != "legacy-dev-1" {
		t.Errorf("Expected device ID 'legacy-dev-1', got '%s'", s2.GetDeviceID())
	}
}

func TestStorage_IdentityCorruption(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	// Write corrupt identity.json
	os.WriteFile(identityPath, []byte(`{corrupt_json`), 0600)

	s := NewSQLiteStorage(dbPath, identityPath, 1024*1024*10)
	err := s.Init(context.Background())
	if err == nil {
		t.Fatalf("Init should fail on corrupt identity.json")
	}
}

func TestStorage_IntegrityAndSequenceRollback(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	s.Init(context.Background())
	defer s.Close()

	ev1 := &events.CanonicalEvent{
		EventID:  "shared-id",
		Severity: "INFO",
		Source:   "payload1",
	}

	err := s.Store(context.Background(), ev1)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if ev1.SeqNo != 1 {
		t.Errorf("Expected SeqNo 1, got %d", ev1.SeqNo)
	}

	// Try to insert same ID but different payload
	ev2 := &events.CanonicalEvent{
		EventID:  "shared-id",
		Severity: "INFO",
		Source:   "payload2",
	}
	err = s.Store(context.Background(), ev2)
	if err == nil {
		t.Fatalf("Store should fail with integrity error")
	}

	// Next event should get SeqNo 2 (no sequence leak)
	ev3 := &events.CanonicalEvent{
		EventID:  "unique-id-3",
		Severity: "INFO",
		Source:   "payload3",
	}
	err = s.Store(context.Background(), ev3)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if ev3.SeqNo != 2 {
		t.Errorf("Expected SeqNo 2, got %d", ev3.SeqNo)
	}
}

func TestStorage_EventIDOwnership(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	s.Init(context.Background())
	defer s.Close()

	// 1. Missing EventID fails
	evEmpty := &events.CanonicalEvent{
		Severity: "INFO",
		Source:   "test1",
	}
	err := s.Store(context.Background(), evEmpty)
	if err == nil {
		t.Fatalf("Store should fail if EventID is empty")
	}
	if !errors.Is(err, ErrStoreFailedBeforeCommit) {
		t.Errorf("Expected ErrStoreFailedBeforeCommit for missing EventID, got %v", err)
	}

	// 2. Existing EventID survives unchanged
	originalID := "fixed-event-id-1234"
	evWithID := &events.CanonicalEvent{
		EventID:  originalID,
		Severity: "INFO",
		Source:   "test2",
	}
	err = s.Store(context.Background(), evWithID)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if evWithID.EventID != originalID {
		t.Errorf("EventID mutated during Store. Expected %s, got %s", originalID, evWithID.EventID)
	}

	// 3. Retry preserves EventID (Idempotency)
	err = s.Store(context.Background(), evWithID)
	if err != nil {
		t.Fatalf("Idempotent Store failed: %v", err)
	}
	if evWithID.EventID != originalID {
		t.Errorf("EventID mutated during idempotent Store")
	}
}

func TestStore_UncertainOutcome(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	s.Init(context.Background())
	defer s.Close()

	ev := &events.CanonicalEvent{
		EventID:  "uncertain-test-1",
		Severity: "INFO",
		Source:   "uncertain",
	}

	// Inject fault to simulate uncertain commit outcome
	faultCount := 0
	s.testFaultInjectCommit = func() error {
		faultCount++
		if faultCount == 1 {
			// Fail the first commit to simulate ambiguity
			return errors.New("simulated I/O error during commit")
		}
		// Second attempt succeeds
		return nil
	}

	// 1st attempt should fail with ErrStoreUncertain
	err := s.Store(context.Background(), ev)
	if err == nil {
		t.Fatalf("Expected Store to fail due to injected fault")
	}
	if !errors.Is(err, ErrStoreUncertain) {
		t.Errorf("Expected ErrStoreUncertain, got %v", err)
	}

	// 2nd attempt (Retry) should succeed
	err = s.Store(context.Background(), ev)
	if err != nil {
		t.Fatalf("Expected retry to succeed, got %v", err)
	}

	if faultCount != 2 {
		t.Errorf("Expected test fault hook to be called twice, got %d", faultCount)
	}

	// Verify exactly one durable record was created despite the uncertain outcome and retry
	var count int
	s.db.QueryRow("SELECT count(*) FROM events WHERE event_id = ?", ev.EventID).Scan(&count)
	if count != 1 {
		t.Errorf("Expected exactly 1 durable event, got %d", count)
	}
}

func TestStore_EventIDPreservation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	s.Init(context.Background())
	defer s.Close()

	originalID := "exact-preservation-123"
	ev := &events.CanonicalEvent{
		EventID:  originalID,
		Severity: "INFO",
		Source:   "preservation",
	}

	err := s.Store(context.Background(), ev)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	retrieved, err := s.GetPendingEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetPendingEvents failed: %v", err)
	}
	if len(retrieved) != 1 {
		t.Fatalf("Expected 1 pending event, got %d", len(retrieved))
	}
	if retrieved[0].EventID != originalID {
		t.Errorf("EventID modified during retrieval. Expected %s, got %s", originalID, retrieved[0].EventID)
	}
}

func TestStore_ErrorWrapping_PreservesUnderlyingCause(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	s.Init(context.Background())
	defer s.Close()

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	ev := &events.CanonicalEvent{
		EventID:  "canceled-test-1",
		Severity: "INFO",
		Source:   "cancellation",
	}

	err := s.Store(canceledCtx, ev)
	if err == nil {
		t.Fatalf("Expected Store to fail on canceled context")
	}

	// Invariant from Section 4:
	// errors.Is(err, ErrStoreFailedBeforeCommit) must be true
	// AND errors.Is(err, context.Canceled) must also be true
	if !errors.Is(err, ErrStoreFailedBeforeCommit) {
		t.Errorf("Expected errors.Is(err, ErrStoreFailedBeforeCommit) to be true, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected errors.Is(err, context.Canceled) to be true, got %v", err)
	}
}

func TestStorage_RetryTimestamp_UTCAndMigration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer s.Close()

	// 1. Store a test event
	ev := &events.CanonicalEvent{
		EventID:  "retry-ev-1",
		Severity: "INFO",
		Source:   "test-retry",
	}
	if err := s.Store(context.Background(), ev); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// 2. Simulate legacy timestamp writes:
	// 2a. Legacy timestamp with positive offset (+0530) and monotonic suffix
	legacyTS := "2026-09-07 00:26:53.3862089 +0530 IST m=+10.022039201"
	_, err := s.db.Exec("UPDATE events SET next_attempt_at = ? WHERE event_id = ?", legacyTS, ev.EventID)
	if err != nil {
		t.Fatalf("Failed to write legacy timestamp: %v", err)
	}

	// 2b. Legacy timestamp with negative offset (-0400)
	evNeg := &events.CanonicalEvent{
		EventID:  "retry-ev-neg",
		Severity: "INFO",
		Source:   "test-retry-neg",
	}
	if err := s.Store(context.Background(), evNeg); err != nil {
		t.Fatalf("Store evNeg failed: %v", err)
	}
	legacyNegTS := "2026-09-06 14:56:53 -0400 EDT m=+5.123"
	if _, err := s.db.Exec("UPDATE events SET next_attempt_at = ? WHERE event_id = ?", legacyNegTS, evNeg.EventID); err != nil {
		t.Fatalf("Failed to write legacy negative offset: %v", err)
	}

	// 2c. Malformed unparseable timestamp
	evBad := &events.CanonicalEvent{
		EventID:  "retry-ev-bad",
		Severity: "INFO",
		Source:   "test-retry-bad",
	}
	if err := s.Store(context.Background(), evBad); err != nil {
		t.Fatalf("Store evBad failed: %v", err)
	}
	if _, err := s.db.Exec("UPDATE events SET next_attempt_at = 'garbage-unparseable-date' WHERE event_id = ?", evBad.EventID); err != nil {
		t.Fatalf("Failed to write malformed timestamp: %v", err)
	}

	// 3. Run migration
	if err := s.migrateRetryTimestamps(context.Background()); err != nil {
		t.Fatalf("migrateRetryTimestamps failed: %v", err)
	}

	// 3a. Verify +0530 migrated correctly
	var migratedTS string
	if err := s.db.QueryRow("SELECT next_attempt_at FROM events WHERE event_id = ?", ev.EventID).Scan(&migratedTS); err != nil {
		t.Fatalf("Failed to read migrated timestamp: %v", err)
	}

	tParsed, ok := parseRetryTimestamp(migratedTS)
	if !ok {
		t.Fatalf("Failed to parse migrated timestamp '%s'", migratedTS)
	}
	expectedInstant := time.Date(2026, 9, 6, 18, 56, 53, 0, time.UTC)
	if !tParsed.Equal(expectedInstant) {
		t.Errorf("Expected migrated instant %v, got %v", expectedInstant, tParsed)
	}

	// 3b. Verify -0400 migrated to exact same instant
	var migratedNegTS string
	if err := s.db.QueryRow("SELECT next_attempt_at FROM events WHERE event_id = ?", evNeg.EventID).Scan(&migratedNegTS); err != nil {
		t.Fatalf("Failed to read migrated negative timestamp: %v", err)
	}
	tNegParsed, ok := parseRetryTimestamp(migratedNegTS)
	if !ok || !tNegParsed.Equal(expectedInstant) {
		t.Errorf("Expected negative offset migrated to %v, got %v (raw: %s)", expectedInstant, tNegParsed, migratedNegTS)
	}

	// 3c. Verify malformed row was migrated to immediate eligibility (in past)
	var migratedBadTS string
	if err := s.db.QueryRow("SELECT next_attempt_at FROM events WHERE event_id = ?", evBad.EventID).Scan(&migratedBadTS); err != nil {
		t.Fatalf("Failed to read migrated malformed timestamp: %v", err)
	}
	tBadParsed, ok := parseRetryTimestamp(migratedBadTS)
	if !ok || !tBadParsed.Before(time.Now().UTC()) {
		t.Errorf("Expected malformed timestamp to be migrated to past time, got %v", tBadParsed)
	}

	// 3d. Verify idempotency of migration: running migration again produces no errors and leaves rows valid
	if err := s.migrateRetryTimestamps(context.Background()); err != nil {
		t.Fatalf("Second run of migrateRetryTimestamps failed: %v", err)
	}

	// Verify all 3 events are now eligible in GetPendingEvents
	pending, err := s.GetPendingEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("GetPendingEvents failed: %v", err)
	}
	if len(pending) != 3 {
		t.Errorf("Expected 3 immediately eligible events after migration, found %d", len(pending))
	}

	// 4. Test MarkFailed produces canonical UTC timestamp
	ev2 := &events.CanonicalEvent{
		EventID:  "retry-ev-2",
		Severity: "INFO",
		Source:   "test-retry-2",
	}
	if err := s.Store(context.Background(), ev2); err != nil {
		t.Fatalf("Store ev2 failed: %v", err)
	}

	if err := s.MarkFailed(context.Background(), ev2.EventID, 60, errors.New("temporary error")); err != nil {
		t.Fatalf("MarkFailed failed: %v", err)
	}

	var markedTS string
	if err := s.db.QueryRow("SELECT next_attempt_at FROM events WHERE event_id = ?", ev2.EventID).Scan(&markedTS); err != nil {
		t.Fatalf("Failed to read marked timestamp: %v", err)
	}

	tMarked, ok := parseRetryTimestamp(markedTS)
	if !ok {
		t.Fatalf("Failed to parse marked timestamp '%s'", markedTS)
	}
	// Marked time must be approx 60 seconds in the future
	if time.Until(tMarked) < 30*time.Second || time.Until(tMarked) > 90*time.Second {
		t.Errorf("Marked timestamp %v is not approximately 60s in the future", tMarked)
	}
}

func TestStorage_RetryArithmetic_BoundedOverflow(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s := NewSQLiteStorage(dbPath, "", 1024*1024*10)
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer s.Close()

	ev := &events.CanonicalEvent{
		EventID:  "overflow-retry-1",
		Severity: "INFO",
		Source:   "overflow-test",
	}
	if err := s.Store(context.Background(), ev); err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Set extreme attempt_count that would overflow 1 << attemptCount (e.g. 65)
	_, err := s.db.Exec("UPDATE events SET attempt_count = 65 WHERE event_id = ?", ev.EventID)
	if err != nil {
		t.Fatalf("Setup attempt_count failed: %v", err)
	}

	// Must not panic or produce negative/overflow duration
	if err := s.MarkFailed(context.Background(), ev.EventID, 0, errors.New("timeout")); err != nil {
		t.Fatalf("MarkFailed failed on high attemptCount: %v", err)
	}

	var attemptCount int
	var nextAttemptAt string
	if err := s.db.QueryRow("SELECT attempt_count, next_attempt_at FROM events WHERE event_id = ?", ev.EventID).Scan(&attemptCount, &nextAttemptAt); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if attemptCount != 66 {
		t.Errorf("Expected attempt_count 66, got %d", attemptCount)
	}
	tNext, ok := parseRetryTimestamp(nextAttemptAt)
	if !ok {
		t.Fatalf("Expected valid timestamp, got '%s'", nextAttemptAt)
	}
	// For saturated backoff (3600s + max 10s jitter), delay should be bounded around 1 hour
	delay := time.Until(tNext)
	if delay < 3500*time.Second || delay > 3700*time.Second {
		t.Errorf("Expected delay near 3600s, got %v", delay)
	}

	// Test extreme retryAfter (e.g. 999999999 seconds); bounded to maxRetryAfterSeconds (86400s)
	if err := s.MarkFailed(context.Background(), ev.EventID, 999999999, errors.New("server retry-after")); err != nil {
		t.Fatalf("MarkFailed failed on extreme retryAfter: %v", err)
	}
	if err := s.db.QueryRow("SELECT next_attempt_at FROM events WHERE event_id = ?", ev.EventID).Scan(&nextAttemptAt); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	tAfter, _ := parseRetryTimestamp(nextAttemptAt)
	afterDelay := time.Until(tAfter)
	if afterDelay > 86500*time.Second || afterDelay < 86300*time.Second {
		t.Errorf("Expected retryAfter bounded near 86400s, got %v", afterDelay)
	}
}

func TestStorage_IdempotencyBeforeQuotaPruning(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	// Sizable initial quota for setup
	s := NewSQLiteStorage(dbPath, "", 500*1024)
	if err := s.Init(context.Background()); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer s.Close()

	// 1. Store a low-priority DEBUG event
	debugEv := &events.CanonicalEvent{
		EventID:  "debug-ev-1",
		Severity: "DEBUG",
		Source:   "debug-source",
		Metadata: map[string]interface{}{"data": "important diagnostic data"},
	}
	if err := s.Store(context.Background(), debugEv); err != nil {
		t.Fatalf("Store debugEv failed: %v", err)
	}

	// 2. Store a high-priority event
	critEv := &events.CanonicalEvent{
		EventID:  "crit-ev-1",
		Severity: "CRITICAL",
		Source:   "crit-source",
		Metadata: map[string]interface{}{"alert": "something critical"},
	}
	if err := s.Store(context.Background(), critEv); err != nil {
		t.Fatalf("Store critEv failed: %v", err)
	}

	// Artificially restrict quota to trigger extreme pressure for any new writes
	s.quotaBytes = 1000

	// 3. Retry storing critEv (idempotent duplicate) under high storage pressure
	// Under F05 fix: idempotency check must succeed immediately without triggering
	// enforceQuota or deleting the debug event.
	if err := s.Store(context.Background(), critEv); err != nil {
		t.Fatalf("Idempotent Store failed: %v", err)
	}

	// Assert debug event was NOT pruned!
	var debugCount int
	s.db.QueryRow("SELECT COUNT(*) FROM events WHERE event_id = 'debug-ev-1'").Scan(&debugCount)
	if debugCount != 1 {
		t.Errorf("DEBUG event was pruned during an idempotent retry! Expected 1, got %d", debugCount)
	}
}

func TestStorage_IdentityStabilityAcrossRestarts(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	// 1. Initial run: legacy bootstrap file exists
	initialUUID := "original-device-uuid-1111"
	if err := os.WriteFile(identityPath, []byte(fmt.Sprintf(`{"device_id":"%s"}`, initialUUID)), 0600); err != nil {
		t.Fatalf("Failed to create mock identity file: %v", err)
	}

	// First initialization
	s1 := NewSQLiteStorage(dbPath, identityPath, 1024*1024*10)
	if err := s1.Init(context.Background()); err != nil {
		t.Fatalf("s1.Init failed: %v", err)
	}
	id1 := s1.GetDeviceID()
	if id1 != initialUUID {
		t.Fatalf("Expected s1 device ID '%s', got '%s'", initialUUID, id1)
	}
	s1.Close()

	// Verify identity.json was unlinked after migration
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("Expected identity.json to be removed after migration")
	}

	// 2. Second run (restart): identity.json does NOT exist
	s2 := NewSQLiteStorage(dbPath, identityPath, 1024*1024*10)
	if err := s2.Init(context.Background()); err != nil {
		t.Fatalf("s2.Init failed on restart: %v", err)
	}
	id2 := s2.GetDeviceID()
	if id2 != initialUUID {
		t.Fatalf("Identity drifted on restart! Expected '%s', got '%s'", initialUUID, id2)
	}
	s2.Close()

	// 3. Third run (repeated restart)
	s3 := NewSQLiteStorage(dbPath, identityPath, 1024*1024*10)
	if err := s3.Init(context.Background()); err != nil {
		t.Fatalf("s3.Init failed on 3rd start: %v", err)
	}
	id3 := s3.GetDeviceID()
	if id3 != initialUUID {
		t.Fatalf("Identity drifted on 3rd run! Expected '%s', got '%s'", initialUUID, id3)
	}
	s3.Close()
}
