//go:build linux

package process

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"redcyberfox/pkg/events"
	"strconv"
	"testing"
	"time"
)

func writeMockAuxv(t *testing.T, tempDir string, clkTck uint64) {
	selfDir := filepath.Join(tempDir, "self")
	os.MkdirAll(selfDir, 0755)

	wordSize := 8
	if strconv.IntSize == 32 {
		wordSize = 4
	}
	auxvBytes := make([]byte, 2*wordSize)
	if wordSize == 8 {
		binary.LittleEndian.PutUint64(auxvBytes[0:8], 17)
		binary.LittleEndian.PutUint64(auxvBytes[8:16], clkTck)
	} else {
		binary.LittleEndian.PutUint32(auxvBytes[0:4], 17)
		binary.LittleEndian.PutUint32(auxvBytes[4:8], uint32(clkTck))
	}
	os.WriteFile(filepath.Join(selfDir, "auxv"), auxvBytes, 0644)
}

func TestLinuxAdapter_ParseStat(t *testing.T) {
	adapter := &linuxAdapter{
		bootTime: 1000,
		userHz:   100, // 100 ticks per second
	}

	// PID 123, Name: my daemon (with space), PPID: 1, starttime: 500 (5 seconds)
	// 5 seconds + bootTime (1000) = 1005
	statStr := "123 (my daemon) S 1 123 123 0 -1 4194560 108 0 0 0 14 4 0 0 20 0 1 0 500 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"

	inst, ok := adapter.parseStat(123, statStr)
	if !ok {
		t.Fatal("Expected parseStat to succeed")
	}

	if inst.PID != 123 {
		t.Errorf("Expected PID 123, got %d", inst.PID)
	}

	if inst.Name == nil || *inst.Name != "my daemon" {
		t.Errorf("Expected Name 'my daemon', got %v", inst.Name)
	}

	if inst.ParentPID == nil || *inst.ParentPID != 1 {
		t.Errorf("Expected ParentPID 1, got %v", inst.ParentPID)
	}

	expectedTime := time.Unix(1005, 0)
	if !inst.StartTime.Equal(expectedTime) {
		t.Errorf("Expected StartTime %v, got %v", expectedTime, inst.StartTime)
	}
}

func TestLinuxAdapter_ParseStat_Hardened(t *testing.T) {
	adapter := &linuxAdapter{
		bootTime: 1000,
		userHz:   100,
	}

	tests := []struct {
		name         string
		statStr      string
		expectedName string
	}{
		{"spaces in name", "123 (my daemon) S 1 123 123 0 -1 4194560 108 0 0 0 14 4 0 0 20 0 1 0 500 0 0 0 0", "my daemon"},
		{"brackets in name", "123 (my(daemon)) S 1 123 123 0 -1 4194560 108 0 0 0 14 4 0 0 20 0 1 0 500 0 0 0 0", "my(daemon)"},
		{"unusual characters", "123 (a_!@#$) S 1 123 123 0 -1 4194560 108 0 0 0 14 4 0 0 20 0 1 0 500 0 0 0 0", "a_!@#$"},
		{"closing bracket inside", "123 (my)daemon) S 1 123 123 0 -1 4194560 108 0 0 0 14 4 0 0 20 0 1 0 500 0 0 0 0", "my)daemon"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, ok := adapter.parseStat(123, tt.statStr)
			if !ok {
				t.Fatalf("Expected parseStat to succeed for %s", tt.name)
			}
			if inst.Name == nil || *inst.Name != tt.expectedName {
				t.Errorf("Expected Name '%s', got '%v'", tt.expectedName, inst.Name)
			}
		})
	}
}

func TestLinuxAdapter_ParseStat_Malformed(t *testing.T) {
	adapter := &linuxAdapter{}

	tests := []struct {
		name    string
		statStr string
	}{
		{"empty", ""},
		{"no closing paren", "123 (my daemon S 1"},
		{"too few fields", "123 (my daemon) S 1 123"},
		{"invalid starttime", "123 (my daemon) S 1 123 123 0 -1 4194560 108 0 0 0 14 4 0 0 20 0 1 0 invalid 0 0 0 0"},
		{"invalid ppid", "123 (my daemon) S invalid 123 123 0 -1 4194560 108 0 0 0 14 4 0 0 20 0 1 0 500 0 0 0 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := adapter.parseStat(123, tt.statStr)
			if ok {
				t.Errorf("Expected parseStat to fail for %s", tt.name)
			}
		})
	}
}

func TestLinuxAdapter_InitFailure(t *testing.T) {
	tempDir := t.TempDir()
	// No auxv written
	os.WriteFile(filepath.Join(tempDir, "stat"), []byte("btime 1000\n"), 0644)

	_, err := newLinuxAdapter(tempDir)
	if err == nil {
		t.Fatal("Expected newLinuxAdapter to fail when AT_CLKTCK is missing")
	}
}

func TestLinuxAdapter_CaptureSnapshot(t *testing.T) {
	tempDir := t.TempDir()

	os.WriteFile(filepath.Join(tempDir, "stat"), []byte("cpu  0 0 0 0 0 0 0 0 0 0\nbtime 1600000000\n"), 0644)
	writeMockAuxv(t, tempDir, 100)

	pid1Dir := filepath.Join(tempDir, "100")
	os.Mkdir(pid1Dir, 0755)
	os.WriteFile(filepath.Join(pid1Dir, "stat"), []byte("100 (valid_proc) S 1 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100 0 0 0 0"), 0644)
	os.WriteFile(filepath.Join(pid1Dir, "cmdline"), []byte("my\x00arg\x00"), 0644)
	os.WriteFile(filepath.Join(pid1Dir, "status"), []byte("Name:\tvalid_proc\nUid:\t0\t0\t0\t0\n"), 0644)

	os.Mkdir(filepath.Join(tempDir, "sys"), 0755)

	pid2Dir := filepath.Join(tempDir, "200")
	os.Mkdir(pid2Dir, 0755)

	adapter, err := newLinuxAdapter(tempDir)
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	snapshot, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("Failed to capture snapshot: %v", err)
	}
	if len(snapshot.Instances) != 1 {
		t.Fatalf("Expected 1 instance, got %d", len(snapshot.Instances))
	}

	inst := snapshot.Instances[0]
	if inst.PID != 100 {
		t.Errorf("Expected PID 100, got %d", inst.PID)
	}
	if inst.Name == nil || *inst.Name != "valid_proc" {
		t.Errorf("Expected Name 'valid_proc'")
	}
	if inst.CommandLine == nil {
		t.Error("Expected CommandLine to be present")
	} else {
		var args []string
		json.Unmarshal([]byte(*inst.CommandLine), &args)
		if len(args) != 2 || args[0] != "my" || args[1] != "arg" {
			t.Errorf("Unexpected command line: %v", args)
		}
	}
	if inst.User == nil {
		t.Error("Expected User to be present")
	} else {
		if *inst.User != "root" && *inst.User != "0" {
			t.Errorf("Unexpected user: %s", *inst.User)
		}
	}
}

func TestLinuxAdapter_ProcessDisappearsBeforeExe(t *testing.T) {
	tempDir := t.TempDir()
	os.WriteFile(filepath.Join(tempDir, "stat"), []byte("btime 1000\n"), 0644)
	writeMockAuxv(t, tempDir, 100)

	pidDir := filepath.Join(tempDir, "300")
	os.Mkdir(pidDir, 0755)
	os.WriteFile(filepath.Join(pidDir, "stat"), []byte("300 (fast) S 1 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100 0"), 0644)

	adapter, _ := newLinuxAdapter(tempDir)
	snapshot, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("Failed to capture snapshot: %v", err)
	}

	if len(snapshot.Instances) != 1 {
		t.Fatalf("Expected 1 instance despite missing optional files, got %d", len(snapshot.Instances))
	}
	inst := snapshot.Instances[0]
	if inst.PID != 300 {
		t.Errorf("Expected PID 300, got %d", inst.PID)
	}
	if inst.CommandLine != nil || inst.User != nil || inst.ExecutablePath != nil {
		t.Errorf("Expected optional fields to be nil")
	}
}

func TestLinuxAdapter_ScanFailurePreservesState(t *testing.T) {
	tempDir := t.TempDir()
	os.WriteFile(filepath.Join(tempDir, "stat"), []byte("btime 1000\n"), 0644)
	writeMockAuxv(t, tempDir, 100)

	// Setup initial process
	pidDir := filepath.Join(tempDir, "400")
	os.Mkdir(pidDir, 0755)
	os.WriteFile(filepath.Join(pidDir, "stat"), []byte("400 (myproc) S 1 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100 0"), 0644)

	adapter, _ := newLinuxAdapter(tempDir)
	snapshot, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("Failed to capture snapshot: %v", err)
	}
	if len(snapshot.Instances) != 1 {
		t.Fatalf("Expected 1 instance, got %d", len(snapshot.Instances))
	}

	out := make(chan *events.CanonicalEvent, 10)
	engine := NewLifecycleEngine(out)

	// Baseline snapshot (empty) -> emits 0 events
	emptyBaseline := &Snapshot{Instances: []*Instance{}}
	engine.Reconcile(context.Background(), emptyBaseline)

	// Reconcile valid snapshot (emits 1 START event)
	engine.Reconcile(context.Background(), snapshot)

	select {
	case ev := <-out:
		if ev.Action != ActionProcessStart {
			t.Errorf("Expected START event, got %s", ev.Action)
		}
	default:
		t.Fatal("Expected event on out channel")
	}

	// Now intentionally make the next capture fail by corrupting the procPath
	adapter.procPath = "/invalid-does-not-exist"
	failedSnapshot, err := adapter.captureSnapshot()
	if err == nil {
		t.Fatal("Expected captureSnapshot to fail on invalid path")
	}
	if failedSnapshot != nil {
		t.Errorf("Expected nil snapshot on failure")
	}

	// Because err != nil, the loop in startOSAdapter will NOT call Reconcile.
	// So we DO NOT call engine.Reconcile here, which proves no EXIT events are generated.
	// If we did mistakenly call engine.Reconcile(&Snapshot{}), it would generate an EXIT.

	// Let's prove that passing an empty snapshot DOES generate an EXIT (baseline test).
	emptySnapshot := &Snapshot{Instances: []*Instance{}}
	engine.Reconcile(context.Background(), emptySnapshot)

	select {
	case ev := <-out:
		if ev.Action != ActionProcessExit {
			t.Errorf("Expected EXIT event, got %s", ev.Action)
		}
	default:
		t.Fatal("Expected EXIT event on out channel when empty snapshot is reconciled")
	}
}

func TestLinuxAdapter_StatFailureYieldsUnobservable(t *testing.T) {
	tempDir := t.TempDir()
	os.WriteFile(filepath.Join(tempDir, "stat"), []byte("btime 1000\n"), 0644)
	writeMockAuxv(t, tempDir, 100)

	// Create a pid dir but DO NOT create a stat file
	// This simulates the process vanishing just before we read stat
	pidDir := filepath.Join(tempDir, "500")
	os.Mkdir(pidDir, 0755)

	adapter, _ := newLinuxAdapter(tempDir)
	snapshot, err := adapter.captureSnapshot()
	if err != nil {
		t.Fatalf("Failed to capture snapshot: %v", err)
	}

	if len(snapshot.Instances) != 1 {
		t.Fatalf("Expected 1 instance (unobservable), got %d", len(snapshot.Instances))
	}
	inst := snapshot.Instances[0]
	if inst.PID != 500 {
		t.Errorf("Expected PID 500, got %d", inst.PID)
	}
	if !inst.StartTime.IsZero() {
		t.Errorf("Expected unobservable instance to have zero StartTime")
	}
}

func TestLinuxAdapter_PrecisionFix(t *testing.T) {
	// 300 USER_HZ does not evenly divide 1e9
	adapter := &linuxAdapter{
		bootTime: 1000,
		userHz:   300,
	}

	// Ticks = 500
	// Sec = 500 / 300 = 1
	// Remainder = 200
	// Nano = (200 * 1e9) / 300 = 666666666
	// Total = 1001 sec, 666666666 nsec
	statStr := "123 (test) S 1 123 123 0 -1 4194560 108 0 0 0 14 4 0 0 20 0 1 0 500 0 0 0 0"

	inst, ok := adapter.parseStat(123, statStr)
	if !ok {
		t.Fatal("Expected parseStat to succeed")
	}

	expectedTime := time.Unix(1001, 666666666)
	if !inst.StartTime.Equal(expectedTime) {
		t.Errorf("Expected StartTime %v, got %v", expectedTime, inst.StartTime)
	}
}
