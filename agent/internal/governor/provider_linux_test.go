//go:build linux

package governor

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLinuxProvider_CPU(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	nowFunc := func() time.Time { return now }

	provInt, err := NewLinuxProvider(nil, nowFunc)
	if err != nil {
		t.Fatal(err)
	}
	prov := provInt.(*linuxProvider)

	// Mock getrusage
	mockUs := int64(1000) // 1 ms
	prov.getrusage = func() (int64, error) {
		return mockUs, nil
	}

	// 1. First sample should be unavailable but baseline initialized
	cpu, sampledAt, err := prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpu.Valid {
		t.Fatal("first CPU sample should be invalid")
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 2. Normal sample (50%)
	// advance time by 10ms, process used 5ms per core
	now = now.Add(10 * time.Millisecond)
	mockUs += 5000 * int64(prov.numCPU) // 5ms per core = 50%

	cpu, sampledAt, err = prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cpu.Valid {
		t.Fatal("second CPU sample should be valid")
	}
	if cpu.Value < 49.0 || cpu.Value > 51.0 {
		t.Fatalf("expected ~50%% cpu, got %f", cpu.Value)
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 3. Exceed 100% -> Clamped
	now = now.Add(10 * time.Millisecond)
	mockUs += 20000 * int64(prov.numCPU) // advanced by 20ms process time in 10ms wall time
	cpu, sampledAt, err = prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cpu.Valid || cpu.Value != 100.0 {
		t.Fatalf("expected 100.0 clamped, got valid=%v val=%f", cpu.Valid, cpu.Value)
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 4. Invalid elapsed wall interval (zero elapsed time)
	cpu, sampledAt, err = prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpu.Valid {
		t.Fatal("zero elapsed time should return invalid measurement")
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 5. Negative process CPU-time delta (counter discontinuity) -> unavailable, advances baseline
	now = now.Add(10 * time.Millisecond)
	mockUs -= 1000 // counter went backwards
	cpu, sampledAt, err = prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpu.Valid {
		t.Fatal("negative process CPU delta must produce unavailable measurement, not valid 0%")
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 6. Safe baseline recovery on subsequent positive interval
	now = now.Add(10 * time.Millisecond)
	mockUs += 5000 * int64(prov.numCPU) // 5ms per core = 50%
	cpu, sampledAt, err = prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cpu.Valid {
		t.Fatal("CPU measurement should recover and be valid after baseline advancement")
	}
	if cpu.Value < 49.0 || cpu.Value > 51.0 {
		t.Fatalf("expected ~50%% cpu after recovery, got %f", cpu.Value)
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}
}

func TestNormalizeCPULinux(t *testing.T) {
	tests := []struct {
		name          string
		elapsedProcUs int64
		elapsedWallNs int64
		numCPU        int
		wantVal       float64
		wantValid     bool
	}{
		{"zero elapsed wall", 1000, 0, 2, 0.0, false},
		{"negative elapsed wall", 1000, -100, 2, 0.0, false},
		{"normal 50%", 500, 1000000, 1, 50.0, true},
		{"normal 100%", 1000, 1000000, 1, 100.0, true},
		{"exceed 100%", 2000, 1000000, 1, 100.0, true},
		{"negative elapsed proc", -100, 1000000, 1, 0.0, false},
		{"zero numCPU", 500, 1000000, 0, 0.0, false},
		{"negative numCPU", 500, 1000000, -1, 0.0, false},
		{"max safe elapsed proc (boundary)", math.MaxInt64 / 1000, 1000000, 1, 100.0, true},
		{"overflow boundary plus 1", (math.MaxInt64 / 1000) + 1, 1000000, 1, 0.0, false},
		{"overflow MaxInt64", math.MaxInt64, 1000000, 1, 0.0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotVal, gotValid := normalizeCPULinux(tc.elapsedProcUs, tc.elapsedWallNs, tc.numCPU)
			if gotValid != tc.wantValid {
				t.Errorf("got valid %v, want %v", gotValid, tc.wantValid)
			}
			if gotVal != tc.wantVal {
				t.Errorf("got val %f, want %f", gotVal, tc.wantVal)
			}
		})
	}
}

func TestLinuxProvider_Memory(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	provInt, _ := NewLinuxProvider(nil, func() time.Time { return now })
	prov := provInt.(*linuxProvider)
	prov.pageSize = 4096

	prov.readStatm = func() (string, error) {
		return "1000 200 100 20 0 50 0\n", nil
	}

	mem, sampledAt, err := prov.SampleMemory(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !mem.Valid {
		t.Fatal("expected valid memory sample")
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}
	expectedRss := uint64(200 * 4096)
	if mem.Value != expectedRss {
		t.Fatalf("expected rss %d, got %d", expectedRss, mem.Value)
	}

	// Invalid format
	prov.readStatm = func() (string, error) {
		return "1000\n", nil
	}
	_, _, err = prov.SampleMemory(ctx)
	if err == nil {
		t.Fatal("expected error on invalid statm format")
	}
}

func TestLinuxProvider_Memory_OverflowAndInvalidPageSize(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	provInt, _ := NewLinuxProvider(nil, func() time.Time { return now })
	prov := provInt.(*linuxProvider)

	// 1. Invalid page size <= 0
	prov.pageSize = 0
	prov.readStatm = func() (string, error) {
		return "1000 200 100 20 0 50 0\n", nil
	}
	_, _, err := prov.SampleMemory(ctx)
	if err == nil {
		t.Fatal("expected error for page size <= 0")
	}

	// 2. Deterministic overflow test
	prov.pageSize = 4096
	prov.readStatm = func() (string, error) {
		// 18446744073709551615 is math.MaxUint64, which exceeds math.MaxUint64 / 4096
		return "1000 18446744073709551615 100 20 0 50 0\n", nil
	}
	_, _, err = prov.SampleMemory(ctx)
	if err == nil {
		t.Fatal("expected error on deterministic rss byte conversion overflow")
	}
}

func TestLinuxProvider_Storage(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "agent.db")
	err := os.WriteFile(dbPath, []byte("data"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	nowFunc := func() time.Time { return now }
	provInt, _ := NewLinuxProvider([]string{dbPath}, nowFunc)
	prov := provInt.(*linuxProvider)

	mockIoContent := "rchar: 12345\nwrite_bytes: 100\n"
	prov.readIo = func() (string, error) {
		return mockIoContent, nil
	}

	// 1. First sample should give total used but invalid write rate, and return injected timestamp
	used, writeBps, sampledAt, err := prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !used.Valid || used.Value != 4 {
		t.Fatalf("expected valid used space of 4, got valid=%v value=%d", used.Valid, used.Value)
	}
	if writeBps.Valid {
		t.Fatal("first write bytes per sec sample should be invalid")
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 2. Unchanged counters over 2 seconds
	now = now.Add(2 * time.Second)
	used, writeBps, sampledAt, err = prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !used.Valid || used.Value != 4 {
		t.Fatalf("expected valid used space of 4, got %v", used)
	}
	if !writeBps.Valid || writeBps.Value != 0.0 {
		t.Fatalf("expected 0 bps, got valid=%v value=%f", writeBps.Valid, writeBps.Value)
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 3. Positive deltas: 400 bytes over 4 seconds = 100 B/s
	now = now.Add(4 * time.Second)
	mockIoContent = "rchar: 12345\nwrite_bytes: 500\n"
	used, writeBps, sampledAt, err = prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !writeBps.Valid || writeBps.Value != 100.0 {
		t.Fatalf("expected 100 bps, got valid=%v value=%f", writeBps.Valid, writeBps.Value)
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 4. Discontinuities (counter decreases)
	now = now.Add(1 * time.Second)
	mockIoContent = "rchar: 12345\nwrite_bytes: 50\n"
	_, writeBps, sampledAt, err = prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if writeBps.Valid {
		t.Fatal("discontinuity should result in invalid measurement")
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}
}

func TestLinuxProvider_Storage_SymlinkAndNonRegular(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	// 1. Regular file
	dbPath := filepath.Join(tempDir, "agent.db")
	err := os.WriteFile(dbPath, make([]byte, 2048), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Symlink to regular file
	symlinkPath := filepath.Join(tempDir, "symlink.db")
	err = os.Symlink(dbPath, symlinkPath)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	provInt, err := NewLinuxProvider([]string{symlinkPath}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	prov := provInt.(*linuxProvider)
	prov.readIo = func() (string, error) { return "write_bytes: 100\n", nil }

	used, _, sampledAt, err := prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error resolving symlink to regular file: %v", err)
	}
	if !used.Valid || used.Value != 2048 {
		t.Fatalf("expected used 2048 for symlink to regular file, got valid=%v value=%d", used.Valid, used.Value)
	}
	if !sampledAt.Equal(now) {
		t.Fatalf("expected sampledAt %v, got %v", now, sampledAt)
	}

	// 3. Directory rejection
	provDir, _ := NewLinuxProvider([]string{tempDir}, nil)
	provDir.(*linuxProvider).readIo = func() (string, error) { return "write_bytes: 0\n", nil }
	_, _, _, err = provDir.SampleStorage(ctx)
	if err == nil {
		t.Fatal("expected error when sampling directory storage path")
	}

	// 4. Non-regular device file (/dev/null is a char device)
	if _, statErr := os.Stat("/dev/null"); statErr == nil {
		provDevice, _ := NewLinuxProvider([]string{"/dev/null"}, nil)
		provDevice.(*linuxProvider).readIo = func() (string, error) { return "write_bytes: 0\n", nil }
		_, _, _, err = provDevice.SampleStorage(ctx)
		if err == nil {
			t.Fatal("expected error when sampling non-regular file (/dev/null)")
		}
	}
}

func TestLinuxProvider_StorageDirectory(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	provInt, _ := NewLinuxProvider([]string{tempDir}, nil)
	prov := provInt.(*linuxProvider)
	prov.readIo = func() (string, error) { return "write_bytes: 0", nil }

	_, _, _, err := prov.SampleStorage(ctx)
	if err == nil {
		t.Fatal("expected error when sampling directory storage path")
	}
}

func TestLinuxProvider_ContextCancellation(t *testing.T) {
	provInt, _ := NewLinuxProvider(nil, nil)
	prov := provInt.(*linuxProvider)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	if _, _, err := prov.SampleCPU(ctx); err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, _, err := prov.SampleMemory(ctx); err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if _, _, _, err := prov.SampleStorage(ctx); err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestLinuxProvider_Storage_EmptyPathsAndCallerMutation(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "agent.db")
	err := os.WriteFile(dbPath, []byte("data"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Empty paths rejected in SampleStorage (do not report valid zero footprint)
	provEmpty, err := NewLinuxProvider(nil, nil)
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
	provMut, err := NewLinuxProvider(paths, nil)
	if err != nil {
		t.Fatal(err)
	}
	provMut.(*linuxProvider).readIo = func() (string, error) { return "write_bytes: 100\n", nil }

	paths[0] = filepath.Join(tempDir, "non_existent.db")

	used, _, _, err = provMut.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("expected successful storage sample after caller slice mutation, got: %v", err)
	}
	if !used.Valid || used.Value != 4 {
		t.Fatalf("expected valid size 4, got valid=%v val=%d", used.Valid, used.Value)
	}
}

func TestLinuxProvider_RealSourcesSmoke(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "real_agent.db")
	smokePayload := []byte("ot-soc-real-linux-smoke-payload")
	if err := os.WriteFile(dbPath, smokePayload, 0644); err != nil {
		t.Fatal(err)
	}

	// Uses default provider functions without overriding getrusage, readStatm, or readIo
	mockNow := time.Now()
	nowFunc := func() time.Time { return mockNow }
	prov, err := NewLinuxProvider([]string{dbPath}, nowFunc)
	if err != nil {
		t.Fatalf("failed to create real Linux provider: %v", err)
	}

	// 1. Exercise CPU sampling on actual Linux
	cpu1, ts1, err := prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error on real CPU sample 1: %v", err)
	}
	if cpu1.Valid {
		t.Fatal("first real CPU sample must be invalid (baseline initialization)")
	}
	if ts1.IsZero() {
		t.Fatal("CPU sample 1 timestamp must not be zero")
	}

	mockNow = mockNow.Add(10 * time.Millisecond)
	cpu2, ts2, err := prov.SampleCPU(ctx)
	if err != nil {
		t.Fatalf("unexpected error on real CPU sample 2: %v", err)
	}
	if !cpu2.Valid {
		t.Fatal("second real CPU sample must be valid")
	}
	if cpu2.Value < 0.0 || cpu2.Value > 100.0 {
		t.Fatalf("real CPU value %f out of expected range [0, 100]", cpu2.Value)
	}
	if !ts2.After(ts1) {
		t.Fatalf("expected ts2 (%v) to be after ts1 (%v)", ts2, ts1)
	}

	// 2. Exercise Memory (RSS) sampling on actual Linux
	mem, memTs, err := prov.SampleMemory(ctx)
	if err != nil {
		t.Fatalf("unexpected error on real Memory sample: %v", err)
	}
	if !mem.Valid {
		t.Fatal("real Memory sample must be valid")
	}
	if mem.Value == 0 {
		t.Fatal("real Memory sample RSS must be non-zero for active process")
	}
	if memTs.IsZero() {
		t.Fatal("Memory sample timestamp must not be zero")
	}

	// 3. Exercise Storage sampling on actual Linux
	used1, writeBps1, stTs1, err := prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error on real Storage sample 1: %v", err)
	}
	if !used1.Valid || used1.Value != uint64(len(smokePayload)) {
		t.Fatalf("expected used footprint %d, got valid=%v val=%d", len(smokePayload), used1.Valid, used1.Value)
	}
	if writeBps1.Valid {
		t.Fatal("first real write rate sample must be invalid (baseline initialization)")
	}
	if stTs1.IsZero() {
		t.Fatal("Storage sample 1 timestamp must not be zero")
	}

	mockNow = mockNow.Add(10 * time.Millisecond)
	used2, writeBps2, stTs2, err := prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error on real Storage sample 2: %v", err)
	}
	if !used2.Valid || used2.Value != uint64(len(smokePayload)) {
		t.Fatalf("expected used footprint %d, got valid=%v val=%d", len(smokePayload), used2.Valid, used2.Value)
	}
	if !writeBps2.Valid {
		t.Fatal("second real write rate sample must be valid")
	}
	if writeBps2.Value < 0.0 {
		t.Fatalf("real write throughput cannot be negative: %f", writeBps2.Value)
	}
	if !stTs2.After(stTs1) {
		t.Fatalf("expected stTs2 (%v) to be after stTs1 (%v)", stTs2, stTs1)
	}
}

func TestAccumulateStorageSizeLinux(t *testing.T) {
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

func TestLinuxProvider_Storage_MultipleFiles(t *testing.T) {
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

	provInt, err := NewLinuxProvider([]string{f1, f2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prov := provInt.(*linuxProvider)
	prov.readIo = func() (string, error) { return "write_bytes: 0\n", nil }

	used, _, _, err := prov.SampleStorage(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !used.Valid || used.Value != 300 {
		t.Fatalf("expected used 300, got %v", used)
	}
}
