package ingestion

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

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
