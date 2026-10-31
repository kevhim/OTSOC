//go:build windows

package governor

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modpsapi                 = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = modpsapi.NewProc("GetProcessMemoryInfo")

	modkernel32              = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessIoCounters = modkernel32.NewProc("GetProcessIoCounters")
)

type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type windowsProvider struct {
	mu           sync.Mutex
	storagePaths []string
	numCPU       int
	proc         windows.Handle
	nowFunc      func() time.Time

	lastCPUSampleTime time.Time
	lastProcessTime   int64

	lastStorageSampleTime time.Time
	lastWriteBytes        uint64
}

// NewWindowsProvider creates a new OSResourceProvider for Windows.
func NewWindowsProvider(storagePaths []string, now func() time.Time) (OSResourceProvider, error) {
	if now == nil {
		now = time.Now
	}
	proc, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(storagePaths))
	copy(paths, storagePaths)
	return &windowsProvider{
		storagePaths: paths,
		numCPU:       runtime.NumCPU(),
		proc:         proc,
		nowFunc:      now,
	}, nil
}

// NewOSResourceProvider creates an OSResourceProvider for the current platform (Windows).
func NewOSResourceProvider(storagePaths []string, now func() time.Time) (OSResourceProvider, error) {
	return NewWindowsProvider(storagePaths, now)
}

func (p *windowsProvider) SampleCPU(ctx context.Context) (ValidFloat64, time.Time, error) {
	select {
	case <-ctx.Done():
		return ValidFloat64{}, time.Time{}, ctx.Err()
	default:
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	sampledAt := p.nowFunc()

	var creation, exit, kernel, user windows.Filetime
	err := windows.GetProcessTimes(p.proc, &creation, &exit, &kernel, &user)
	if err != nil {
		return ValidFloat64{}, sampledAt, err
	}

	// 100-ns units
	kTime := (int64(kernel.HighDateTime) << 32) | int64(kernel.LowDateTime)
	uTime := (int64(user.HighDateTime) << 32) | int64(user.LowDateTime)
	totalTime := kTime + uTime

	if p.lastCPUSampleTime.IsZero() {
		p.lastCPUSampleTime = sampledAt
		p.lastProcessTime = totalTime
		return ValidFloat64{Valid: false, Value: 0.0}, sampledAt, nil
	}

	elapsedWallNs := sampledAt.Sub(p.lastCPUSampleTime).Nanoseconds()
	elapsedProcUnits := totalTime - p.lastProcessTime

	p.lastCPUSampleTime = sampledAt
	p.lastProcessTime = totalTime

	cpuPercent, valid := normalizeCPU(elapsedProcUnits, elapsedWallNs, p.numCPU)

	return ValidFloat64{Valid: valid, Value: cpuPercent}, sampledAt, nil
}

func normalizeCPU(elapsedProcUnits, elapsedWallNs int64, numCPU int) (float64, bool) {
	if elapsedWallNs <= 0 || elapsedProcUnits < 0 || numCPU <= 0 || elapsedProcUnits > math.MaxInt64/100 {
		return 0.0, false
	}

	elapsedProcNs := elapsedProcUnits * 100
	cpuFraction := float64(elapsedProcNs) / float64(elapsedWallNs)
	cpuPercent := (cpuFraction / float64(numCPU)) * 100.0

	if cpuPercent > 100.0 {
		cpuPercent = 100.0
	}

	return cpuPercent, true
}

func accumulateStorageSize(totalUsed uint64, fileSize int64) (uint64, error) {
	if fileSize < 0 {
		return 0, fmt.Errorf("negative file size: %d", fileSize)
	}
	fileSizeBytes := uint64(fileSize)
	if fileSizeBytes > math.MaxUint64-totalUsed {
		return 0, fmt.Errorf("storage size addition overflow: %d + %d exceeds math.MaxUint64", totalUsed, fileSizeBytes)
	}
	return totalUsed + fileSizeBytes, nil
}

func (p *windowsProvider) SampleMemory(ctx context.Context) (ValidUint64, time.Time, error) {
	select {
	case <-ctx.Done():
		return ValidUint64{}, time.Time{}, ctx.Err()
	default:
	}

	sampledAt := p.nowFunc()

	var pmc processMemoryCounters
	pmc.CB = uint32(unsafe.Sizeof(pmc))

	ret, _, err := procGetProcessMemoryInfo.Call(
		uintptr(p.proc),
		uintptr(unsafe.Pointer(&pmc)),
		uintptr(pmc.CB),
	)
	if ret == 0 {
		if err != nil {
			if e, ok := err.(syscall.Errno); ok && e == 0 {
				return ValidUint64{}, sampledAt, fmt.Errorf("GetProcessMemoryInfo failed without reporting a Windows error code")
			}
			if e, ok := err.(windows.Errno); ok && e == 0 {
				return ValidUint64{}, sampledAt, fmt.Errorf("GetProcessMemoryInfo failed without reporting a Windows error code")
			}
			return ValidUint64{}, sampledAt, err
		}
		return ValidUint64{}, sampledAt, fmt.Errorf("GetProcessMemoryInfo failed without reporting a Windows error code")
	}

	return ValidUint64{Valid: true, Value: uint64(pmc.WorkingSetSize)}, sampledAt, nil
}

func (p *windowsProvider) SampleStorage(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
	select {
	case <-ctx.Done():
		return ValidUint64{}, ValidFloat64{}, time.Time{}, ctx.Err()
	default:
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	sampledAt := p.nowFunc()

	if len(p.storagePaths) == 0 {
		return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("storage paths configuration is empty: at least one regular file path must be configured")
	}

	var totalUsed uint64
	for _, path := range p.storagePaths {
		info, err := os.Stat(path)
		if err != nil {
			return ValidUint64{}, ValidFloat64{}, sampledAt, err
		}
		if !info.Mode().IsRegular() {
			if info.IsDir() {
				return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("storage path %s is a directory, which is prohibited", path)
			}
			return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("storage path %s is not a regular file, which is prohibited", path)
		}
		var accErr error
		totalUsed, accErr = accumulateStorageSize(totalUsed, info.Size())
		if accErr != nil {
			return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("storage path %s: %w", path, accErr)
		}
	}

	var counters ioCounters
	ret, _, err := procGetProcessIoCounters.Call(
		uintptr(p.proc),
		uintptr(unsafe.Pointer(&counters)),
	)

	if ret == 0 {
		if err != nil {
			if e, ok := err.(syscall.Errno); ok && e == 0 {
				return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("GetProcessIoCounters failed without reporting a Windows error code")
			}
			if e, ok := err.(windows.Errno); ok && e == 0 {
				return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("GetProcessIoCounters failed without reporting a Windows error code")
			}
			return ValidUint64{}, ValidFloat64{}, sampledAt, err
		}
		return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("GetProcessIoCounters failed without reporting a Windows error code")
	}

	writeBytes := counters.WriteTransferCount
	var writeBps float64
	validWrite := false

	if !p.lastStorageSampleTime.IsZero() {
		elapsedSec := sampledAt.Sub(p.lastStorageSampleTime).Seconds()
		if elapsedSec > 0 && writeBytes >= p.lastWriteBytes {
			writeBps = float64(writeBytes-p.lastWriteBytes) / elapsedSec
			validWrite = true
		}
	}

	p.lastStorageSampleTime = sampledAt
	p.lastWriteBytes = writeBytes

	return ValidUint64{Valid: true, Value: totalUsed}, ValidFloat64{Valid: validWrite, Value: writeBps}, sampledAt, nil
}
