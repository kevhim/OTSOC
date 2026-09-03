//go:build linux

package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

func TestLinuxAdapter_CaptureSnapshot(t *testing.T) {
	tempDir := t.TempDir()

	// Write /proc/stat
	procStatPath := filepath.Join(tempDir, "stat")
	os.WriteFile(procStatPath, []byte("cpu  0 0 0 0 0 0 0 0 0 0\nbtime 1600000000\n"), 0644)

	// Create a valid process directory
	pid1Dir := filepath.Join(tempDir, "100")
	os.Mkdir(pid1Dir, 0755)
	os.WriteFile(filepath.Join(pid1Dir, "stat"), []byte("100 (valid_proc) S 1 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100 0 0 0 0"), 0644)
	os.WriteFile(filepath.Join(pid1Dir, "cmdline"), []byte("my\x00arg\x00"), 0644)
	os.WriteFile(filepath.Join(pid1Dir, "status"), []byte("Name:\tvalid_proc\nUid:\t0\t0\t0\t0\n"), 0644)
	// exe is a symlink, simulate it if supported or skip

	// Create a non-numeric directory (should be ignored)
	os.Mkdir(filepath.Join(tempDir, "sys"), 0755)

	// Create a process directory with missing stat (disappeared race condition)
	pid2Dir := filepath.Join(tempDir, "200")
	os.Mkdir(pid2Dir, 0755)
	// No stat file

	adapter, err := newLinuxAdapter(tempDir)
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	snapshot := adapter.captureSnapshot()
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

	pidDir := filepath.Join(tempDir, "300")
	os.Mkdir(pidDir, 0755)
	os.WriteFile(filepath.Join(pidDir, "stat"), []byte("300 (fast) S 1 0 0 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100 0"), 0644)
	// Do not write anything else to simulate disappearance during optional metadata

	adapter, _ := newLinuxAdapter(tempDir)
	snapshot := adapter.captureSnapshot()

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
