//go:build windows

package process

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

// --- Mocks ---

type mockWindowsAPI struct {
	enumFunc      func() ([]ProcessEntry, error)
	startFunc     func(pid uint32) (time.Time, error)
	execPathFunc  func(pid uint32) (string, error)
	userSIDFunc   func(pid uint32) (string, error)
	resolveFunc   func(sid string) (string, error)
	cleanupCalled bool
}

func (m *mockWindowsAPI) EnumerateProcesses() ([]ProcessEntry, error) {
	if m.enumFunc != nil {
		return m.enumFunc()
	}
	return nil, nil
}

func (m *mockWindowsAPI) GetProcessStartTime(pid uint32) (time.Time, error) {
	if m.startFunc != nil {
		return m.startFunc(pid)
	}
	return time.Time{}, errors.New("not found")
}

func (m *mockWindowsAPI) GetExecutablePath(pid uint32) (string, error) {
	if m.execPathFunc != nil {
		return m.execPathFunc(pid)
	}
	return "", errors.New("not found")
}

func (m *mockWindowsAPI) GetProcessUserSID(pid uint32) (string, error) {
	if m.userSIDFunc != nil {
		return m.userSIDFunc(pid)
	}
	return "", errors.New("not found")
}

func (m *mockWindowsAPI) ResolveSID(sid string) (string, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(sid)
	}
	return "", errors.New("not found")
}

// --- Tests ---

func TestFiletimeToTime(t *testing.T) {
	tests := []struct {
		name    string
		ft      windows.Filetime
		want    time.Time
		wantErr bool
	}{
		{
			name: "valid known time",
			// Jan 1, 1970 00:00:00 UTC
			// 11644473600 seconds between 1601 and 1970
			// 116444736000000000 100-ns intervals = 0x019DB1DED53E8000
			ft: windows.Filetime{
				LowDateTime:  0xd53e8000,
				HighDateTime: 0x019db1de,
			},
			want:    time.Unix(0, 0),
			wantErr: false,
		},
		{
			name:    "zero time",
			ft:      windows.Filetime{LowDateTime: 0, HighDateTime: 0},
			want:    time.Time{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filetimeToTime(tt.ft)
			if (err != nil) != tt.wantErr {
				t.Errorf("filetimeToTime() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !got.Equal(tt.want) {
				t.Errorf("filetimeToTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWindowsAdapter_captureSnapshot_Success(t *testing.T) {
	now := time.Now()
	mockAPI := &mockWindowsAPI{
		enumFunc: func() ([]ProcessEntry, error) {
			return []ProcessEntry{
				{PID: 100, PPID: 1, Name: "test.exe"},
			}, nil
		},
		startFunc: func(pid uint32) (time.Time, error) {
			if pid == 100 {
				return now, nil
			}
			return time.Time{}, errors.New("not found")
		},
		execPathFunc: func(pid uint32) (string, error) {
			return "C:\\test.exe", nil
		},
		userSIDFunc: func(pid uint32) (string, error) {
			return "S-1-5-18", nil
		},
		resolveFunc: func(sid string) (string, error) {
			if sid == "S-1-5-18" {
				return "SYSTEM", nil
			}
			return "", errors.New("not found")
		},
	}

	adapter := &windowsAdapter{
		api: mockAPI,
	}

	snap, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(snap.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(snap.Instances))
	}

	inst := snap.Instances[0]
	if inst.PID != 100 {
		t.Errorf("expected PID 100, got %d", inst.PID)
	}
	if !inst.StartTime.Equal(now) {
		t.Errorf("expected start time %v, got %v", now, inst.StartTime)
	}
	if *inst.Name != "test.exe" {
		t.Errorf("expected name 'test.exe', got '%s'", *inst.Name)
	}
	if *inst.ParentPID != 1 {
		t.Errorf("expected PPID 1, got %d", *inst.ParentPID)
	}
	if *inst.ExecutablePath != "C:\\test.exe" {
		t.Errorf("expected path 'C:\\test.exe', got '%s'", *inst.ExecutablePath)
	}
	if *inst.User != "SYSTEM" {
		t.Errorf("expected user 'SYSTEM', got '%s'", *inst.User)
	}
}

func TestWindowsAdapter_captureSnapshot_DisappearingProcess(t *testing.T) {
	now := time.Now()
	mockAPI := &mockWindowsAPI{
		enumFunc: func() ([]ProcessEntry, error) {
			return []ProcessEntry{
				{PID: 100, Name: "exists.exe"},
				{PID: 101, Name: "disappears.exe"},
			}, nil
		},
		startFunc: func(pid uint32) (time.Time, error) {
			if pid == 100 {
				return now, nil
			}
			// PID 101 disappeared before StartTime could be queried
			return time.Time{}, errors.New("process exited")
		},
	}

	adapter := &windowsAdapter{
		api: mockAPI,
	}

	snap, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(snap.Instances) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(snap.Instances))
	}
	if snap.Instances[0].PID != 100 {
		t.Errorf("expected PID 100 to survive")
	}
	if snap.Instances[1].PID != 101 {
		t.Errorf("expected PID 101 to be appended as unobservable")
	}
	if !snap.Instances[1].StartTime.IsZero() {
		t.Errorf("expected PID 101 to have zero StartTime")
	}
}

func TestWindowsAdapter_captureSnapshot_AccessDeniedMetadata(t *testing.T) {
	now := time.Now()
	mockAPI := &mockWindowsAPI{
		enumFunc: func() ([]ProcessEntry, error) {
			return []ProcessEntry{
				{PID: 100, Name: "restricted.exe"},
			}, nil
		},
		startFunc: func(pid uint32) (time.Time, error) {
			return now, nil // start time succeeds with limited query
		},
		execPathFunc: func(pid uint32) (string, error) {
			return "", errors.New("access denied")
		},
		userSIDFunc: func(pid uint32) (string, error) {
			return "", errors.New("access denied")
		},
	}

	adapter := &windowsAdapter{
		api: mockAPI,
	}

	snap, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(snap.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(snap.Instances))
	}

	inst := snap.Instances[0]
	if inst.PID != 100 {
		t.Errorf("expected PID 100, got %d", inst.PID)
	}
	if inst.ExecutablePath != nil {
		t.Errorf("expected ExecutablePath to be nil on access denied")
	}
	if inst.User != nil {
		t.Errorf("expected User to be nil on access denied")
	}
}

func TestWindowsAdapter_captureSnapshot_EmptySuccessful(t *testing.T) {
	mockAPI := &mockWindowsAPI{
		enumFunc: func() ([]ProcessEntry, error) {
			return []ProcessEntry{}, nil
		},
	}

	adapter := &windowsAdapter{
		api: mockAPI,
	}

	snap, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap == nil {
		t.Fatalf("expected snapshot, got nil")
	}
	if len(snap.Instances) != 0 {
		t.Fatalf("expected 0 instances")
	}
}

func TestWindowsAdapter_captureSnapshot_Failure(t *testing.T) {
	mockAPI := &mockWindowsAPI{
		enumFunc: func() ([]ProcessEntry, error) {
			return nil, errors.New("toolhelp failed")
		},
	}

	adapter := &windowsAdapter{
		api: mockAPI,
	}

	snap, err := adapter.captureSnapshot()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if snap != nil {
		t.Fatal("expected nil snapshot on failure")
	}
}

func TestWindowsAPI_EnumerateProcesses_PartialFailure(t *testing.T) {
	// We want to test realWindowsAPI.EnumerateProcesses() explicitly
	// by overriding the Toolhelp seams.
	originalCreate := sysCreateToolhelp32Snapshot
	originalFirst := sysProcess32First
	originalNext := sysProcess32Next

	defer func() {
		sysCreateToolhelp32Snapshot = originalCreate
		sysProcess32First = originalFirst
		sysProcess32Next = originalNext
	}()

	sysCreateToolhelp32Snapshot = func(flags uint32, pid uint32) (windows.Handle, error) {
		return windows.Handle(1234), nil // mock handle
	}

	sysProcess32First = func(handle windows.Handle, entry *windows.ProcessEntry32) error {
		entry.ProcessID = 100
		return nil
	}

	callCount := 0
	sysProcess32Next = func(handle windows.Handle, entry *windows.ProcessEntry32) error {
		callCount++
		if callCount == 1 {
			entry.ProcessID = 101
			return nil
		}
		// Simulate unexpected failure on the 2nd call to Next
		return errors.New("unexpected error in Process32Next")
	}

	api := &realWindowsAPI{}
	entries, err := api.EnumerateProcesses()
	if err == nil {
		t.Fatal("expected error from unexpected Process32Next failure, got nil")
	}
	if err.Error() != "unexpected error in Process32Next" {
		t.Fatalf("expected specific error, got %v", err)
	}
	if entries != nil {
		t.Fatalf("expected entries to be nil on partial failure, but got partial list of length %d", len(entries))
	}
}

func TestWindowsAdapter_PIDReuse(t *testing.T) {
	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)
	ctx := context.Background()

	t1 := time.Now().Add(-10 * time.Minute)
	t2 := time.Now()

	// Snapshot 1: PID 500, StartTime T1
	snap1 := &Snapshot{
		Instances: []*Instance{
			{PID: 500, StartTime: t1},
		},
	}
	engine.Reconcile(ctx, snap1)

	// Since it's the first snapshot (baseline), NO events are emitted.
	if len(out) != 0 {
		t.Fatalf("expected 0 events on baseline, got %d", len(out))
	}

	// Snapshot 2: PID 500 reused, StartTime T2
	snap2 := &Snapshot{
		Instances: []*Instance{
			{PID: 500, StartTime: t2},
		},
	}
	engine.Reconcile(ctx, snap2)

	// Should emit PROCESS_EXIT for T1, and PROCESS_START for T2
	if len(out) != 2 {
		t.Fatalf("expected 2 events on pid reuse, got %d", len(out))
	}

	ev1 := <-out
	if ev1.Action != "PROCESS_EXIT" {
		t.Errorf("expected PROCESS_EXIT, got %s", ev1.Action)
	}

	ev2 := <-out
	if ev2.Action != "PROCESS_START" {
		t.Errorf("expected PROCESS_START, got %s", ev2.Action)
	}
}

func TestStartOSAdapter_Concurrency(t *testing.T) {
	// Restore defaultOSAPI after test
	originalAPI := defaultOSAPI
	defer func() { defaultOSAPI = originalAPI }()

	var concurrent int32
	var maxConcurrent int32
	var count int32

	enterCh := make(chan struct{})
	releaseCh := make(chan struct{})

	mockAPI := &mockWindowsAPI{
		enumFunc: func() ([]ProcessEntry, error) {
			current := atomic.AddInt32(&concurrent, 1)
			defer atomic.AddInt32(&concurrent, -1)

			for {
				max := atomic.LoadInt32(&maxConcurrent)
				if current <= max || atomic.CompareAndSwapInt32(&maxConcurrent, max, current) {
					break
				}
			}

			// Block the first enumeration
			if atomic.AddInt32(&count, 1) == 1 {
				close(enterCh)
				<-releaseCh
			}

			return []ProcessEntry{}, nil
		},
	}
	defaultOSAPI = mockAPI

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Config{ProcessInterval: "1ms"} // extremely short tick
	collector := NewCollector(cfg)

	out := make(chan *events.CanonicalEvent, 100)
	go func() {
		_ = collector.Start(ctx, out)
	}()

	// 1. first enumeration enters -> signal test
	<-enterCh

	// Give a short time for the extremely short (1ms) ticks to pile up
	time.Sleep(20 * time.Millisecond)

	// 2. verify second enumeration has NOT started
	if max := atomic.LoadInt32(&maxConcurrent); max > 1 {
		t.Errorf("expected max concurrent enumerations to be 1, got %d", max)
	}

	// 3. release first enumeration -> allow loop to proceed
	close(releaseCh)

	// wait for completion
	time.Sleep(10 * time.Millisecond)
}
func TestStartOSAdapter_EnumerationFailure(t *testing.T) {
	originalAPI := defaultOSAPI
	defer func() { defaultOSAPI = originalAPI }()

	var callCount int32
	mockAPI := &mockWindowsAPI{
		enumFunc: func() ([]ProcessEntry, error) {
			count := atomic.AddInt32(&callCount, 1)
			if count == 1 {
				// First snapshot: baseline (empty)
				return []ProcessEntry{}, nil
			} else if count == 2 {
				// Second snapshot: success
				return []ProcessEntry{
					{PID: 100, Name: "A.exe"},
					{PID: 101, Name: "B.exe"},
					{PID: 102, Name: "C.exe"},
				}, nil
			} else if count == 3 {
				// Third snapshot: fails part-way through
				return nil, errors.New("unexpected error in Process32Next")
			}
			// Fourth snapshot: success, state restored
			return []ProcessEntry{
				{PID: 100, Name: "A.exe"},
				{PID: 101, Name: "B.exe"},
				{PID: 102, Name: "C.exe"},
			}, nil
		},
		startFunc: func(pid uint32) (time.Time, error) {
			return time.Unix(0, 0), nil
		},
	}
	defaultOSAPI = mockAPI

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := &config.Config{ProcessInterval: "10ms"}
	collector := NewCollector(cfg)

	out := make(chan *events.CanonicalEvent, 100) // startOSAdapter takes chan<- *events.CanonicalEvent
	go func() {
		_ = collector.Start(ctx, out)
	}()

	// Wait for multiple ticks
	time.Sleep(100 * time.Millisecond)
	cancel()

	var evs []*events.CanonicalEvent
	for len(out) > 0 {
		evs = append(evs, <-out)
	}

	// We expect exactly 3 PROCESS_START events from the first successful snapshot
	// The second failed snapshot should yield NO PROCESS_EXIT events
	// The third successful snapshot should just reconcile the existing 3 processes and emit nothing
	starts := 0
	exits := 0
	for _, ev := range evs {
		if ev.Action == "PROCESS_START" {
			starts++
		}
		if ev.Action == "PROCESS_EXIT" {
			exits++
		}
	}

	if starts != 3 {
		t.Errorf("expected 3 PROCESS_START events, got %d", starts)
	}
	if exits != 0 {
		t.Errorf("expected 0 PROCESS_EXIT events (due to failed enumeration), got %d", exits)
	}
}
