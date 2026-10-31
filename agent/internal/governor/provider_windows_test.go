//go:build windows

package governor

import (
	"context"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsProvider_Basic(t *testing.T) {
	tempDir, err := ioutil.TempDir("", "gov-win-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	f, err := os.Create(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(make([]byte, 1024))
	f.Close()

	now := time.Now()
	nowFunc := func() time.Time { return now }

	prov, err := NewWindowsProvider([]string{dbPath}, nowFunc)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Memory Test
	mem, ts, err := prov.SampleMemory(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mem.Valid || mem.Value == 0 {
		t.Fatal("memory should be valid and > 0")
	}
	if !ts.Equal(now) {
		t.Fatalf("expected timestamp %v, got %v", now, ts)
	}

	// CPU Test - First sample should be invalid (unavailable)
	cpu, ts, err := prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpu.Valid {
		t.Fatal("first CPU sample should be invalid")
	}
	if !ts.Equal(now) {
		t.Fatalf("expected timestamp %v, got %v", now, ts)
	}

	// CPU Test - Advance time by 0, elapsed <= 0
	cpu, _, err = prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpu.Valid {
		t.Fatal("CPU sample with 0 elapsed time should be invalid")
	}

	// CPU Test - Recovery after invalid measurement
	now = now.Add(50 * time.Millisecond)
	cpu, ts, err = prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cpu.Valid {
		t.Fatal("CPU sample should recover and be valid after elapsed time advances")
	}
	if !ts.Equal(now) {
		t.Fatalf("expected timestamp %v, got %v", now, ts)
	}

	// Storage Test - Directory should return error
	provWithDir, err := NewWindowsProvider([]string{tempDir}, nowFunc)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = provWithDir.SampleStorage(ctx)
	if err == nil {
		t.Fatal("expected error when sampling directory storage path")
	}

	// Storage Test - First sample should have invalid write rate
	now = now.Add(time.Second)
	used, writeBps, ts, err := prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ts.Equal(now) {
		t.Fatalf("expected timestamp %v, got %v", now, ts)
	}
	if !used.Valid || used.Value != 1024 {
		t.Fatalf("expected used 1024, got %v", used)
	}
	if writeBps.Valid {
		t.Fatal("first storage sample write rate should be invalid")
	}

	// Second Storage Test - unchanged counter
	now = now.Add(2 * time.Second)
	used, writeBps, ts, err = prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ts.Equal(now) {
		t.Fatalf("expected timestamp %v, got %v", now, ts)
	}
	if !writeBps.Valid {
		t.Fatal("second storage sample write rate should be valid")
	}
	if writeBps.Value != 0 {
		t.Fatalf("expected 0 B/s for unchanged counters, got %f", writeBps.Value)
	}

	// Third Storage Test - elapsed <= 0
	_, writeBps, ts, err = prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ts.Equal(now) {
		t.Fatalf("expected timestamp %v, got %v", now, ts)
	}
	if writeBps.Valid {
		t.Fatal("storage write rate should be invalid for 0 elapsed time")
	}

	// Fourth Storage Test - increase
	f, _ = os.OpenFile(dbPath, os.O_APPEND|os.O_WRONLY, 0644)
	f.Write(make([]byte, 1024*1024))
	f.Sync()
	f.Close()

	now = now.Add(2 * time.Second)
	used, writeBps, ts, err = prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ts.Equal(now) {
		t.Fatalf("expected timestamp %v, got %v", now, ts)
	}
	if !writeBps.Valid {
		t.Fatal("storage write rate should be valid after write")
	}
	if writeBps.Value <= 0 {
		t.Fatalf("expected positive write rate after writing data, got %f", writeBps.Value)
	}
	if !used.Valid || used.Value != 1024+1024*1024 {
		t.Fatalf("expected used %d, got %v", 1024+1024*1024, used)
	}
}

func TestNormalizeCPU(t *testing.T) {
	tests := []struct {
		name             string
		elapsedProcUnits int64
		elapsedWallNs    int64
		numCPU           int
		wantVal          float64
		wantValid        bool
	}{
		{"zero elapsed wall", 1000, 0, 2, 0.0, false},
		{"negative elapsed wall", 1000, -100, 2, 0.0, false},
		{"normal 50%", 50, 10000, 1, 50.0, true},
		{"normal 100%", 100, 10000, 1, 100.0, true},
		{"exceed 100%", 200, 10000, 1, 100.0, true},
		{"negative elapsed proc", -100, 10000, 2, 0.0, false},
		{"zero numCPU", 50, 10000, 0, 0.0, false},
		{"negative numCPU", 50, 10000, -1, 0.0, false},
		{"max safe elapsed proc (boundary)", math.MaxInt64 / 100, 10000, 1, 100.0, true},
		{"overflow boundary plus 1", (math.MaxInt64 / 100) + 1, 10000, 1, 0.0, false},
		{"overflow MaxInt64", math.MaxInt64, 10000, 1, 0.0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotVal, gotValid := normalizeCPU(tc.elapsedProcUnits, tc.elapsedWallNs, tc.numCPU)
			if gotValid != tc.wantValid {
				t.Errorf("got valid %v, want %v", gotValid, tc.wantValid)
			}
			if gotVal != tc.wantVal {
				t.Errorf("got val %f, want %f", gotVal, tc.wantVal)
			}
		})
	}
}

func TestWindowsProvider_Storage_EmptyPathsAndCallerMutation(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "agent.db")
	err := os.WriteFile(dbPath, []byte("data"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Empty paths rejected in SampleStorage (do not report valid zero footprint)
	provEmpty, err := NewWindowsProvider(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	used, _, _, err := provEmpty.SampleStorage(ctx)
	if err == nil {
		t.Fatal("expected error on empty storage paths")
	}
	if used.Valid {
		t.Fatal("empty storage paths must not report a valid zero footprint")
	}

	// 2. Caller slice mutation after construction does not affect provider
	paths := []string{dbPath}
	provMut, err := NewWindowsProvider(paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	paths[0] = filepath.Join(tempDir, "non_existent.db")

	used, _, _, err = provMut.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("expected successful storage sample after caller slice mutation, got: %v", err)
	}
	if !used.Valid || used.Value != 4 {
		t.Fatalf("expected valid size 4, got valid=%v val=%d", used.Valid, used.Value)
	}
}

func TestWindowsProvider_ContextCancellation(t *testing.T) {
	prov, err := NewWindowsProvider(nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	if _, _, err := prov.SampleCPU(ctx); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, _, err := prov.SampleMemory(ctx); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, _, _, err := prov.SampleStorage(ctx); err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestAccumulateStorageSizeWindows(t *testing.T) {
	tests := []struct {
		name      string
		totalUsed uint64
		fileSize  int64
		wantTotal uint64
		wantErr   bool
	}{
		{
			name:      "normal size addition",
			totalUsed: 1000,
			fileSize:  500,
			wantTotal: 1500,
			wantErr:   false,
		},
		{
			name:      "exact representable boundary MaxInt64",
			totalUsed: 0,
			fileSize:  math.MaxInt64,
			wantTotal: uint64(math.MaxInt64),
			wantErr:   false,
		},
		{
			name:      "exact representable boundary MaxUint64",
			totalUsed: math.MaxUint64 - 1000,
			fileSize:  1000,
			wantTotal: math.MaxUint64,
			wantErr:   false,
		},
		{
			name:      "overflowing addition boundary plus 1",
			totalUsed: math.MaxUint64 - 1000,
			fileSize:  1001,
			wantTotal: 0,
			wantErr:   true,
		},
		{
			name:      "overflowing addition on MaxUint64",
			totalUsed: math.MaxUint64,
			fileSize:  1,
			wantTotal: 0,
			wantErr:   true,
		},
		{
			name:      "negative input size -1",
			totalUsed: 1000,
			fileSize:  -1,
			wantTotal: 0,
			wantErr:   true,
		},
		{
			name:      "negative input size MinInt64",
			totalUsed: 0,
			fileSize:  math.MinInt64,
			wantTotal: 0,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotTotal, err := accumulateStorageSize(tc.totalUsed, tc.fileSize)
			if (err != nil) != tc.wantErr {
				t.Fatalf("accumulateStorageSize(%d, %d) err = %v, wantErr %v", tc.totalUsed, tc.fileSize, err, tc.wantErr)
			}
			if !tc.wantErr && gotTotal != tc.wantTotal {
				t.Fatalf("accumulateStorageSize(%d, %d) = %d, want %d", tc.totalUsed, tc.fileSize, gotTotal, tc.wantTotal)
			}
		})
	}
}

func TestWindowsProvider_Storage_MultipleFiles(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	f1 := filepath.Join(tempDir, "f1.db")
	f2 := filepath.Join(tempDir, "f2.db")
	if err := os.WriteFile(f1, make([]byte, 100), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}

	prov, err := NewWindowsProvider([]string{f1, f2}, nil)
	if err != nil {
		t.Fatal(err)
	}

	used, _, _, err := prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !used.Valid || used.Value != 300 {
		t.Fatalf("expected used 300, got %v", used)
	}
}
