package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
		Severity: "INFO",
		Source:   "test1",
	}

	err := s.Store(context.Background(), ev)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}
	if ev.EventID == "" {
		t.Errorf("EventID should be populated")
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
			Severity: "DEBUG",
			Source:   "large payload to trigger quota 0000000000000000000000000000000",
		}
		s.Store(context.Background(), ev)
	}

	critEv := &events.CanonicalEvent{
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
