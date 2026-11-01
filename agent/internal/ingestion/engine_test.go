package ingestion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"redcyberfox/agent/internal/health"
	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

type mockStorageFailures struct {
	storeErr error
	dlqErr   error
}

func (m *mockStorageFailures) Init(ctx context.Context) error { return nil }
func (m *mockStorageFailures) Store(ctx context.Context, event *events.CanonicalEvent) error {
	if m.storeErr != nil {
		return m.storeErr
	}
	return nil
}
func (m *mockStorageFailures) GetPendingEvents(ctx context.Context, limit int) ([]*events.CanonicalEvent, error) {
	return nil, nil
}
func (m *mockStorageFailures) RemoveEvent(ctx context.Context, eventID string) error { return nil }
func (m *mockStorageFailures) MarkFailed(ctx context.Context, eventID string, retryAfter int, err error) error {
	return nil
}
func (m *mockStorageFailures) MoveToDLQ(ctx context.Context, event *events.CanonicalEvent, failureType, failureReason string) error {
	if m.dlqErr != nil {
		return m.dlqErr
	}
	return nil
}
func (m *mockStorageFailures) GetDeviceID() string { return "mock-device" }
func (m *mockStorageFailures) Close() error        { return nil }

func TestIngestion_EmergencySpill_SerializationFailureDoesNotClaimSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	spillFile := filepath.Join(tmpDir, "spill.log")

	mockStore := &mockStorageFailures{
		storeErr: storage.ErrStoreUncertain,
		dlqErr:   errors.New("dlq db disk full"),
	}

	var fatalCalled bool
	cfg := Config{
		RetryDelay:         1 * time.Millisecond,
		EmergencySpillPath: spillFile,
		OnFatal: func(ev *events.CanonicalEvent, err error) {
			fatalCalled = true
		},
	}
	eng := NewEngine(mockStore, cfg)

	// Event with non-serializable payload (math.NaN() cannot be serialized to standard JSON)
	ev := &events.CanonicalEvent{
		EventID:  "spill-fail-1",
		Severity: "CRITICAL",
		Source:   "test-source",
		Metadata: map[string]interface{}{
			"invalid_number": math.NaN(),
		},
	}

	ctx := context.Background()
	outcome := eng.ProcessEvent(ctx, ctx, ev)

	// Invariant: MUST NOT return OutcomeSpilledDisk on serialization failure!
	if outcome == OutcomeSpilledDisk {
		t.Fatalf("F03 violation: ProcessEvent returned OutcomeSpilledDisk when json.Marshal failed!")
	}
	if outcome != OutcomeDropped {
		t.Errorf("Expected OutcomeDropped, got %v", outcome)
	}
	if !fatalCalled {
		t.Errorf("Expected OnFatal to be invoked upon terminal spill failure")
	}

	// Spill file must not exist or be empty
	if info, err := os.Stat(spillFile); err == nil && info.Size() > 0 {
		t.Errorf("Spill file was written despite serialization failure: size=%d", info.Size())
	}
}

func TestIngestion_EmergencySpill_SuccessfulSpill(t *testing.T) {
	tmpDir := t.TempDir()
	spillFile := filepath.Join(tmpDir, "spill.log")

	mockStore := &mockStorageFailures{
		storeErr: storage.ErrStoreUncertain,
		dlqErr:   errors.New("dlq db disk full"),
	}

	cfg := Config{
		RetryDelay:         1 * time.Millisecond,
		EmergencySpillPath: spillFile,
	}
	eng := NewEngine(mockStore, cfg)

	ev := &events.CanonicalEvent{
		EventID:  "spill-success-1",
		Severity: "CRITICAL",
		Source:   "test-source",
		Metadata: map[string]interface{}{
			"key": "valid_value",
		},
	}

	ctx := context.Background()
	outcome := eng.ProcessEvent(ctx, ctx, ev)

	if outcome != OutcomeSpilledDisk {
		t.Fatalf("Expected OutcomeSpilledDisk, got %v", outcome)
	}

	data, err := os.ReadFile(spillFile)
	if err != nil {
		t.Fatalf("Failed to read spill file: %v", err)
	}
	if len(data) == 0 {
		t.Errorf("Spill file is empty")
	}
}

func TestIngestion_EmergencySpill_PreservesEarlierEvents(t *testing.T) {
	spillFile := filepath.Join(t.TempDir(), "spill.log")
	store := &mockStorageFailures{
		storeErr: storage.ErrStoreUncertain,
		dlqErr:   errors.New("dlq unavailable"),
	}
	eng := NewEngine(store, Config{
		RetryDelay:         1 * time.Millisecond,
		EmergencySpillPath: spillFile,
	})
	ctx := context.Background()
	for _, id := range []string{"first-event", "second-event"} {
		ev := &events.CanonicalEvent{EventID: id, Source: "test"}
		if got := eng.ProcessEvent(ctx, ctx, ev); got != OutcomeSpilledDisk {
			t.Fatalf("spilling %s: got %s", id, got)
		}
	}

	data, err := os.ReadFile(spillFile)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 preserved records, got %d: %s", len(lines), data)
	}
	for i, want := range []string{"first-event", "second-event"} {
		var got events.CanonicalEvent
		if err := json.Unmarshal([]byte(lines[i]), &got); err != nil {
			t.Fatalf("invalid JSON line %d: %v", i, err)
		}
		if got.EventID != want {
			t.Errorf("line %d id = %s, want %s", i, got.EventID, want)
		}
	}
}

func TestIngestion_EmergencySpill_ConcurrentWrites(t *testing.T) {
	spillFile := filepath.Join(t.TempDir(), "concurrent_spill.log")
	store := &mockStorageFailures{
		storeErr: storage.ErrStoreUncertain,
		dlqErr:   errors.New("dlq unavailable"),
	}
	eng := NewEngine(store, Config{
		RetryDelay:         1 * time.Millisecond,
		EmergencySpillPath: spillFile,
	})

	const numWorkers = 20
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx := context.Background()
			ev := &events.CanonicalEvent{
				EventID:  fmt.Sprintf("concurrent-event-%02d", id),
				Source:   "test-source",
				Severity: "CRITICAL",
			}
			outcome := eng.ProcessEvent(ctx, ctx, ev)
			if outcome != OutcomeSpilledDisk {
				errCh <- fmt.Errorf("worker %d got outcome %v, want OutcomeSpilledDisk", id, outcome)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	data, err := os.ReadFile(spillFile)
	if err != nil {
		t.Fatalf("Failed to read spill file: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != numWorkers {
		t.Fatalf("expected %d distinct spill lines, got %d", numWorkers, len(lines))
	}

	seen := make(map[string]bool)
	for idx, line := range lines {
		var ev events.CanonicalEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("corrupted JSON on line %d: %v (raw: %s)", idx, err, line)
		}
		if !strings.HasPrefix(ev.EventID, "concurrent-event-") {
			t.Errorf("unexpected event ID format on line %d: %s", idx, ev.EventID)
		}
		seen[ev.EventID] = true
	}

	if len(seen) != numWorkers {
		t.Errorf("expected %d unique event IDs in spill file, got %d", numWorkers, len(seen))
	}
}

func TestIngestion_EndpointIdentityLifecycleAcrossRestarts(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	identityPath := filepath.Join(tmpDir, "identity.json")

	// 1. Initial boot: legacy identity bootstrap file exists
	initialID := "initial-device-guid-12345"
	if err := os.WriteFile(identityPath, []byte(`{"device_id":"`+initialID+`"}`), 0600); err != nil {
		t.Fatalf("Failed to create identity file: %v", err)
	}

	// First run: Storage initializes, migrates identity into SQLite, unlinks identity.json
	db1 := storage.NewSQLiteStorage(dbPath, identityPath, 10*1024*1024)
	if err := db1.Init(context.Background()); err != nil {
		t.Fatalf("db1.Init failed: %v", err)
	}
	devID1 := db1.GetDeviceID()
	if devID1 != initialID {
		t.Fatalf("Expected devID1 '%s', got '%s'", initialID, devID1)
	}

	// Verify health manager identity matches SQLite authoritative device ID on initial boot
	healthMgr1 := health.NewManager(devID1, "tenant-test", "site-test", nil)
	if healthMgr1.DeviceID() != initialID {
		t.Fatalf("Health manager identity mismatch on boot 1: expected '%s', got '%s'", initialID, healthMgr1.DeviceID())
	}

	// Endpoint wires devID1 into Ingestion Engine AssetID (matching updated main.go wiring)
	eng1 := NewEngine(db1, Config{
		TenantID: "tenant-test",
		SiteID:   "site-test",
		AssetID:  devID1,
	})

	ev1 := &events.CanonicalEvent{
		EventID:  "run1-ev-1",
		Severity: "INFO",
		Source:   "test",
	}
	ctx := context.Background()
	outcome1 := eng1.ProcessEvent(ctx, ctx, ev1)
	if outcome1 != OutcomeCommitted {
		t.Fatalf("Event 1 failed to commit: %v", outcome1)
	}

	// Verify ev1 in storage has matching AssetID, DeviceID in database, and SeqNo = 1
	pending1, err := db1.GetPendingEvents(ctx, 10)
	if err != nil || len(pending1) != 1 {
		t.Fatalf("Failed to get pending events: %v (len: %d)", err, len(pending1))
	}
	if pending1[0].AssetID != initialID || pending1[0].SeqNo != 1 {
		t.Errorf("Event 1 mismatch: AssetID=%s, SeqNo=%d", pending1[0].AssetID, pending1[0].SeqNo)
	}
	var dbDevID1 string
	if err := db1.GetDB().QueryRow("SELECT device_id FROM events WHERE event_id = ?", pending1[0].EventID).Scan(&dbDevID1); err != nil {
		t.Fatalf("Failed to query stored device_id: %v", err)
	}
	if dbDevID1 != initialID {
		t.Errorf("Stored device_id mismatch: expected '%s', got '%s'", initialID, dbDevID1)
	}
	db1.Close()

	// Verify identity.json is absent after run 1
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("Expected identity.json to be deleted after migration")
	}

	// 2. Second boot (Endpoint Restart):
	// identity.json is absent! Storage re-opens existing db.
	db2 := storage.NewSQLiteStorage(dbPath, identityPath, 10*1024*1024)
	if err := db2.Init(context.Background()); err != nil {
		t.Fatalf("db2.Init failed on restart: %v", err)
	}
	devID2 := db2.GetDeviceID()
	if devID2 != initialID {
		t.Fatalf("Identity drifted on restart! Expected '%s', got '%s'", initialID, devID2)
	}

	// Verify health manager identity on restart 2
	healthMgr2 := health.NewManager(devID2, "tenant-test", "site-test", nil)
	if healthMgr2.DeviceID() != initialID {
		t.Fatalf("Health manager identity mismatch on restart 2: expected '%s', got '%s'", initialID, healthMgr2.DeviceID())
	}

	// Endpoint constructs ingestion engine using authoritative devID2
	eng2 := NewEngine(db2, Config{
		TenantID: "tenant-test",
		SiteID:   "site-test",
		AssetID:  devID2,
	})

	ev2 := &events.CanonicalEvent{
		EventID:  "run2-ev-2",
		Severity: "INFO",
		Source:   "test",
	}
	outcome2 := eng2.ProcessEvent(ctx, ctx, ev2)
	if outcome2 != OutcomeCommitted {
		t.Fatalf("Event 2 failed to commit on restart: %v", outcome2)
	}

	// Verify ev2 in storage has matching AssetID, DeviceID, and continuous SeqNo = 2
	pending2, err := db2.GetPendingEvents(ctx, 10)
	if err != nil || len(pending2) != 2 {
		t.Fatalf("Failed to get pending events on restart: %v (len: %d)", err, len(pending2))
	}
	if pending2[1].AssetID != initialID || pending2[1].SeqNo != 2 {
		t.Errorf("Event 2 mismatch: AssetID=%s, SeqNo=%d", pending2[1].AssetID, pending2[1].SeqNo)
	}
	var dbDevID2 string
	if err := db2.GetDB().QueryRow("SELECT device_id FROM events WHERE event_id = ?", pending2[1].EventID).Scan(&dbDevID2); err != nil {
		t.Fatalf("Failed to query stored device_id for event 2: %v", err)
	}
	if dbDevID2 != initialID {
		t.Errorf("Stored device_id mismatch for event 2: expected '%s', got '%s'", initialID, dbDevID2)
	}
	db2.Close()

	// 3. Third boot (Conflicting Legacy Identity File Present):
	// A rogue or conflicting identity.json is placed in the filesystem.
	// SQLite must remain authoritative: retain initialID, ignore rogue file, and remove the file.
	conflictingID := "conflicting-rogue-device-99999"
	if err := os.WriteFile(identityPath, []byte(`{"device_id":"`+conflictingID+`"}`), 0600); err != nil {
		t.Fatalf("Failed to write conflicting identity file: %v", err)
	}

	db3 := storage.NewSQLiteStorage(dbPath, identityPath, 10*1024*1024)
	if err := db3.Init(context.Background()); err != nil {
		t.Fatalf("db3.Init failed with conflicting legacy identity: %v", err)
	}
	devID3 := db3.GetDeviceID()
	if devID3 != initialID {
		t.Fatalf("SQLite failed to maintain authoritative identity against conflicting file! Got '%s', expected '%s'", devID3, initialID)
	}

	// Verify conflicting file was unlinked
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("Expected conflicting identity.json to be unlinked after db3.Init")
	}

	// Verify health manager identity matches SQLite authoritative ID
	healthMgr3 := health.NewManager(devID3, "tenant-test", "site-test", nil)
	if healthMgr3.DeviceID() != initialID {
		t.Fatalf("Health manager identity mismatch on restart 3: expected '%s', got '%s'", initialID, healthMgr3.DeviceID())
	}

	eng3 := NewEngine(db3, Config{
		TenantID: "tenant-test",
		SiteID:   "site-test",
		AssetID:  devID3,
	})

	ev3 := &events.CanonicalEvent{
		EventID:  "run3-ev-3",
		Severity: "WARN",
		Source:   "test",
	}
	outcome3 := eng3.ProcessEvent(ctx, ctx, ev3)
	if outcome3 != OutcomeCommitted {
		t.Fatalf("Event 3 failed to commit on restart 3: %v", outcome3)
	}

	pending3, err := db3.GetPendingEvents(ctx, 10)
	if err != nil || len(pending3) != 3 {
		t.Fatalf("Failed to get pending events on restart 3: %v (len: %d)", err, len(pending3))
	}
	if pending3[2].AssetID != initialID || pending3[2].SeqNo != 3 {
		t.Errorf("Event 3 mismatch: AssetID=%s, SeqNo=%d", pending3[2].AssetID, pending3[2].SeqNo)
	}
	var dbDevID3 string
	if err := db3.GetDB().QueryRow("SELECT device_id FROM events WHERE event_id = ?", pending3[2].EventID).Scan(&dbDevID3); err != nil {
		t.Fatalf("Failed to query stored device_id for event 3: %v", err)
	}
	if dbDevID3 != initialID {
		t.Errorf("Stored device_id mismatch for event 3: expected '%s', got '%s'", initialID, dbDevID3)
	}

	// Verify historical events 1 and 2 were not modified or rewritten
	if pending3[0].AssetID != initialID || pending3[0].SeqNo != 1 {
		t.Errorf("Historical event 1 corrupted: AssetID=%s, SeqNo=%d", pending3[0].AssetID, pending3[0].SeqNo)
	}
	if pending3[1].AssetID != initialID || pending3[1].SeqNo != 2 {
		t.Errorf("Historical event 2 corrupted: AssetID=%s, SeqNo=%d", pending3[1].AssetID, pending3[1].SeqNo)
	}
	db3.Close()

	// 4. Fourth boot (Corrupt/Malformed Legacy Identity File):
	// A corrupt legacy file exists. SQLite must still open reliably using its authoritative committed state.
	if err := os.WriteFile(identityPath, []byte(`{invalid-json-payload-corrupt`), 0600); err != nil {
		t.Fatalf("Failed to write corrupt identity file: %v", err)
	}

	db4 := storage.NewSQLiteStorage(dbPath, identityPath, 10*1024*1024)
	if err := db4.Init(context.Background()); err != nil {
		t.Fatalf("db4.Init failed with corrupt legacy identity file present: %v", err)
	}
	devID4 := db4.GetDeviceID()
	if devID4 != initialID {
		t.Fatalf("SQLite identity corrupted by malformed file! Got '%s', expected '%s'", devID4, initialID)
	}
	if _, err := os.Stat(identityPath); !os.IsNotExist(err) {
		t.Fatalf("Expected corrupt identity.json to be unlinked after db4.Init")
	}

	healthMgr4 := health.NewManager(devID4, "tenant-test", "site-test", nil)
	if healthMgr4.DeviceID() != initialID {
		t.Fatalf("Health manager identity mismatch on restart 4: expected '%s', got '%s'", initialID, healthMgr4.DeviceID())
	}

	eng4 := NewEngine(db4, Config{
		TenantID: "tenant-test",
		SiteID:   "site-test",
		AssetID:  devID4,
	})

	ev4 := &events.CanonicalEvent{
		EventID:  "run4-ev-4",
		Severity: "ERROR",
		Source:   "test",
	}
	outcome4 := eng4.ProcessEvent(ctx, ctx, ev4)
	if outcome4 != OutcomeCommitted {
		t.Fatalf("Event 4 failed to commit on restart 4: %v", outcome4)
	}

	pending4, err := db4.GetPendingEvents(ctx, 10)
	if err != nil || len(pending4) != 4 {
		t.Fatalf("Failed to get pending events on restart 4: %v (len: %d)", err, len(pending4))
	}
	if pending4[3].AssetID != initialID || pending4[3].SeqNo != 4 {
		t.Errorf("Event 4 mismatch: AssetID=%s, SeqNo=%d", pending4[3].AssetID, pending4[3].SeqNo)
	}
	db4.Close()
}
