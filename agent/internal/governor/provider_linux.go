//go:build linux

package governor

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type linuxProvider struct {
	mu           sync.Mutex
	storagePaths []string
	numCPU       int
	nowFunc      func() time.Time
	pageSize     int

	lastCPUSampleTime time.Time
	lastProcessTime   int64

	lastStorageSampleTime time.Time
	lastWriteBytes        uint64

	// Exposed for testing
	readStatm func() (string, error)
	readIo    func() (string, error)
	getrusage func() (int64, error)
}

// NewLinuxProvider creates a new OSResourceProvider for Linux.
func NewLinuxProvider(storagePaths []string, now func() time.Time) (OSResourceProvider, error) {
	if now == nil {
		now = time.Now
	}
	paths := make([]string, len(storagePaths))
	copy(paths, storagePaths)
	return &linuxProvider{
		storagePaths: paths,
		numCPU:       runtime.NumCPU(),
		nowFunc:      now,
		pageSize:     os.Getpagesize(),
		readStatm:    defaultReadStatm,
		readIo:       defaultReadIo,
		getrusage:    defaultGetrusage,
	}, nil
}

// NewOSResourceProvider creates an OSResourceProvider for the current platform (Linux).
func NewOSResourceProvider(storagePaths []string, now func() time.Time) (OSResourceProvider, error) {
	return NewLinuxProvider(storagePaths, now)
}

// defaultReadStatm reads /proc/self/statm which provides OS-reported memory statistics in pages.
// Field 1 (resident) provides an OS-reported RSS estimate.
func defaultReadStatm() (string, error) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// defaultReadIo reads /proc/self/io for process I/O statistics.
// Attribution caveat: Under Linux kernel accounting, /proc/self/io write_bytes includes I/O from
// child processes that have exited and been waited on (e.g., wait4) by this process.
func defaultReadIo() (string, error) {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func defaultGetrusage() (int64, error) {
	var usage syscall.Rusage
	err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	if err != nil {
		return 0, err
	}
	// Microseconds
	return int64(usage.Utime.Sec+usage.Stime.Sec)*1000000 + int64(usage.Utime.Usec+usage.Stime.Usec), nil
}

func normalizeCPULinux(elapsedProcUs, elapsedWallNs int64, numCPU int) (float64, bool) {
	if elapsedWallNs <= 0 || elapsedProcUs < 0 || numCPU <= 0 || elapsedProcUs > math.MaxInt64/1000 {
		return 0.0, false
	}

	elapsedProcNs := elapsedProcUs * 1000
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

func (p *linuxProvider) SampleCPU(ctx context.Context) (ValidFloat64, time.Time, error) {
	select {
	case <-ctx.Done():
		return ValidFloat64{}, time.Time{}, ctx.Err()
	default:
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	sampledAt := p.nowFunc()

	totalUs, err := p.getrusage()
	if err != nil {
		return ValidFloat64{}, sampledAt, err
	}

	if p.lastCPUSampleTime.IsZero() {
		p.lastCPUSampleTime = sampledAt
		p.lastProcessTime = totalUs
		return ValidFloat64{Valid: false, Value: 0.0}, sampledAt, nil
	}

	elapsedWallNs := sampledAt.Sub(p.lastCPUSampleTime).Nanoseconds()
	elapsedProcUs := totalUs - p.lastProcessTime

	p.lastCPUSampleTime = sampledAt
	p.lastProcessTime = totalUs

	cpuPercent, valid := normalizeCPULinux(elapsedProcUs, elapsedWallNs, p.numCPU)

	return ValidFloat64{Valid: valid, Value: cpuPercent}, sampledAt, nil
}

// SampleMemory returns the current Resident Set Size (RSS) of the agent process in bytes.
// Note: /proc/self/statm provides an OS-reported RSS estimate (in pages) rather than an exact
// real-time physical memory measurement.
func (p *linuxProvider) SampleMemory(ctx context.Context) (ValidUint64, time.Time, error) {
	select {
	case <-ctx.Done():
		return ValidUint64{}, time.Time{}, ctx.Err()
	default:
	}

	sampledAt := p.nowFunc()

	data, err := p.readStatm()
	if err != nil {
		return ValidUint64{}, sampledAt, err
	}

	fields := bytes.Fields([]byte(data))
	if len(fields) < 2 {
		return ValidUint64{}, sampledAt, fmt.Errorf("invalid statm format")
	}

	rssPages, err := strconv.ParseUint(string(fields[1]), 10, 64)
	if err != nil {
		return ValidUint64{}, sampledAt, fmt.Errorf("failed to parse rss from statm: %w", err)
	}

	if p.pageSize <= 0 {
		return ValidUint64{}, sampledAt, fmt.Errorf("invalid page size: %d", p.pageSize)
	}

	if rssPages > math.MaxUint64/uint64(p.pageSize) {
		return ValidUint64{}, sampledAt, fmt.Errorf("rss byte conversion overflow: pages=%d, pageSize=%d", rssPages, p.pageSize)
	}

	rssBytes := rssPages * uint64(p.pageSize)
	return ValidUint64{Valid: true, Value: rssBytes}, sampledAt, nil
}

func (p *linuxProvider) SampleStorage(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
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

	ioData, err := p.readIo()
	if err != nil {
		return ValidUint64{}, ValidFloat64{}, sampledAt, err
	}

	var writeBytes uint64
	found := false
	lines := bytes.Split([]byte(ioData), []byte("\n"))
	for _, line := range lines {
		if bytes.HasPrefix(line, []byte("write_bytes:")) {
			parts := bytes.Fields(line)
			if len(parts) == 2 {
				val, parseErr := strconv.ParseUint(string(parts[1]), 10, 64)
				if parseErr == nil {
					writeBytes = val
					found = true
				}
			}
			break
		}
	}

	if !found {
		return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("write_bytes not found in /proc/self/io")
	}

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
