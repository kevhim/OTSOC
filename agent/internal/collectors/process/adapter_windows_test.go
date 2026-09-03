//go:build windows

package process

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows"

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

	if len(snap.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(snap.Instances))
	}
	if snap.Instances[0].PID != 100 {
		t.Errorf("expected PID 100 to survive")
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
		t.Fatalf("expected error, got nil")
	}
	if snap != nil {
		t.Fatalf("expected nil snapshot on failure")
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
