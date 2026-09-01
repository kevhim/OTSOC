package forwarder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"redcyberfox/pkg/events"
)

type MockStorage struct {
	events  []*events.CanonicalEvent
	removed []string
	failed  []string
	dlq     []string
	mu      sync.Mutex
}

func (m *MockStorage) GetPendingEvents(ctx context.Context, limit int) ([]*events.CanonicalEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events, nil
}

func (m *MockStorage) RemoveEvent(ctx context.Context, eventID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, eventID)
	return nil
}

func (m *MockStorage) MarkFailed(ctx context.Context, eventID string, retryAfter int, err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = append(m.failed, eventID)
	return nil
}

func (m *MockStorage) MoveToDLQ(ctx context.Context, event *events.CanonicalEvent, failureType, failureReason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dlq = append(m.dlq, event.EventID)
	return nil
}

func (m *MockStorage) Init(ctx context.Context) error                                { return nil }
func (m *MockStorage) Store(ctx context.Context, event *events.CanonicalEvent) error { return nil }
func (m *MockStorage) GetDeviceID() string                                           { return "mock-device" }
func (m *MockStorage) Close() error                                                  { return nil }

func TestForwarder_202Accepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	store := &MockStorage{
		events: []*events.CanonicalEvent{
			{EventID: "ev-1", TenantID: "tenant-1"},
		},
	}
	f := NewForwarder(srv.URL, "tenant-1", store)

	f.processPending(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.removed) != 1 || store.removed[0] != "ev-1" {
		t.Errorf("Event should be removed on 202")
	}
}

func TestForwarder_400BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	store := &MockStorage{
		events: []*events.CanonicalEvent{
			{EventID: "ev-2", TenantID: "tenant-1"},
		},
	}
	f := NewForwarder(srv.URL, "tenant-1", store)

	f.processPending(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.dlq) != 1 || store.dlq[0] != "ev-2" {
		t.Errorf("Event should be moved to DLQ on 400")
	}
}

func TestForwarder_429TooManyRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	store := &MockStorage{
		events: []*events.CanonicalEvent{
			{EventID: "ev-3", TenantID: "tenant-1"},
		},
	}
	f := NewForwarder(srv.URL, "tenant-1", store)

	f.processPending(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failed) != 1 || store.failed[0] != "ev-3" {
		t.Errorf("Event should be marked failed on 429")
	}
}

func TestForwarder_LostResponseTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	store := &MockStorage{
		events: []*events.CanonicalEvent{
			{EventID: "ev-4", TenantID: "tenant-1"},
		},
	}
	f := NewForwarder(srv.URL, "tenant-1", store)
	f.httpClient.Timeout = 10 * time.Millisecond // fast timeout

	f.processPending(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.failed) != 1 || store.failed[0] != "ev-4" {
		t.Errorf("Event should be marked failed on timeout")
	}
	if len(store.removed) != 0 {
		t.Errorf("Event should not be removed on timeout")
	}
}

func TestForwarder_TenantMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	store := &MockStorage{
		events: []*events.CanonicalEvent{
			{EventID: "ev-tenant", TenantID: "wrong-tenant"},
		},
	}
	f := NewForwarder(srv.URL, "correct-tenant", store)
	f.processPending(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.dlq) != 1 || store.dlq[0] != "ev-tenant" {
		t.Errorf("Event should be moved to DLQ on tenant mismatch")
	}
	if len(store.removed) > 0 {
		t.Errorf("Event should not be removed")
	}
}

type ErrorMockStorage struct {
	MockStorage
	failRemove bool
	failDLQ    bool
}

func (m *ErrorMockStorage) RemoveEvent(ctx context.Context, eventID string) error {
	if m.failRemove {
		return context.DeadlineExceeded // arbitrary error
	}
	return m.MockStorage.RemoveEvent(ctx, eventID)
}

func (m *ErrorMockStorage) MoveToDLQ(ctx context.Context, event *events.CanonicalEvent, failureType, failureReason string) error {
	if m.failDLQ {
		return context.DeadlineExceeded // arbitrary error
	}
	return m.MockStorage.MoveToDLQ(ctx, event, failureType, failureReason)
}

func TestForwarder_LocalRemovalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	store := &ErrorMockStorage{
		MockStorage: MockStorage{
			events: []*events.CanonicalEvent{
				{EventID: "ev-remove-fail", TenantID: "tenant-1"},
			},
		},
		failRemove: true,
	}
	f := NewForwarder(srv.URL, "tenant-1", store)
	f.processPending(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	// The event remains in the store and wasn't removed (removed count is 0)
	// It is not moved to DLQ, just logged.
	if len(store.removed) != 0 {
		t.Errorf("Expected 0 removed since RemoveEvent failed")
	}
}

func TestForwarder_MoveToDLQFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest) // 400
	}))
	defer srv.Close()

	store := &ErrorMockStorage{
		MockStorage: MockStorage{
			events: []*events.CanonicalEvent{
				{EventID: "ev-dlq-fail", TenantID: "tenant-1"},
			},
		},
		failDLQ: true,
	}
	f := NewForwarder(srv.URL, "tenant-1", store)
	f.processPending(context.Background())

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.dlq) != 0 {
		t.Errorf("Expected 0 dlq since MoveToDLQ failed")
	}
	if len(store.removed) != 0 {
		t.Errorf("Original event must not be removed")
	}
}

func TestForwarder_Lifecycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	store := &MockStorage{}
	f := NewForwarder(srv.URL, "tenant-1", store)

	// Test Start
	err := f.Start(context.Background())
	if err != nil {
		t.Fatalf("Expected no error on first start, got %v", err)
	}

	// Test double Start
	err = f.Start(context.Background())
	if err == nil {
		t.Fatalf("Expected error on second start")
	}
	if err.Error() != "forwarder already started" {
		t.Fatalf("Expected 'forwarder already started' error, got %v", err)
	}

	// Test Stop
	err = f.Stop()
	if err != nil {
		t.Fatalf("Expected no error on stop, got %v", err)
	}

	// Test idempotent Stop
	err = f.Stop()
	if err != nil {
		t.Fatalf("Expected no error on second stop, got %v", err)
	}
}
