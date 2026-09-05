//go:build linux

package process

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// linuxAdapter encapsulates the Linux-specific /proc polling mechanism.
type linuxAdapter struct {
	procPath string
	bootTime int64
	userHz   int64
	// uidCache stores the UID to Username mapping for a single reconciliation pass
	uidCache map[string]string
}

// startOSAdapter starts the OS-specific polling loop for Linux.
func (c *ProcessCollector) startOSAdapter(ctx context.Context) {
	// Parse the config interval
	interval := 30 * time.Second
	if c.cfg != nil && c.cfg.ProcessInterval != "" {
		if d, err := time.ParseDuration(c.cfg.ProcessInterval); err == nil && d > 0 {
			interval = d
		} else {
			log.Printf("ProcessCollector: invalid process_interval %q, defaulting to 30s", c.cfg.ProcessInterval)
		}
	}

	adapter, err := newLinuxAdapter("/proc")
	if err != nil {
		log.Printf("ProcessCollector: failed to initialize Linux adapter: %v", err)
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial reconciliation
	snapshot, err := adapter.captureSnapshot()
	if err != nil {
		log.Printf("ProcessCollector: initial /proc scan failed: %v", err)
	} else {
		c.Reconcile(ctx, snapshot)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := adapter.captureSnapshot()
			if err != nil {
				log.Printf("ProcessCollector: /proc scan failed: %v", err)
				continue
			}
			c.Reconcile(ctx, snapshot)
		}
	}
}

func newLinuxAdapter(procPath string) (*linuxAdapter, error) {
	bootTime, err := getBootTime(procPath)
	if err != nil {
		return nil, fmt.Errorf("could not determine boot time: %w", err)
	}

	userHz, err := getClkTick(procPath)
	if err != nil {
		return nil, fmt.Errorf("could not determine clock ticks: %w", err)
	}

	return &linuxAdapter{
		procPath: procPath,
		bootTime: bootTime,
		userHz:   userHz,
	}, nil
}

func (a *linuxAdapter) captureSnapshot() (*Snapshot, error) {
	a.uidCache = make(map[string]string) // Reset cache for this pass

	entries, err := os.ReadDir(a.procPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", a.procPath, err)
	}

	var instances []*Instance

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		// PID directories are entirely numeric
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		inst := a.parseProcess(pid)
		if inst != nil {
			instances = append(instances, inst)
		} else {
			instances = append(instances, &Instance{PID: pid})
		}
	}

	return &Snapshot{
		Instances: instances,
	}, nil
}

func (a *linuxAdapter) parseProcess(pid int) *Instance {
	pidDir := filepath.Join(a.procPath, strconv.Itoa(pid))

	// 1. Read /proc/<pid>/stat
	statPath := filepath.Join(pidDir, "stat")
	statBytes, err := os.ReadFile(statPath)
	if err != nil {
		// Process may have exited, ignore.
		return nil
	}

	inst, ok := a.parseStat(pid, string(statBytes))
	if !ok {
		return nil
	}

	// 2. Optional Metadata: ExecutablePath
	exePath := filepath.Join(pidDir, "exe")
	target, err := os.Readlink(exePath)
	if err == nil {
		inst.ExecutablePath = &target
	}

	// 3. Optional Metadata: CommandLine
	cmdPath := filepath.Join(pidDir, "cmdline")
	if cmdBytes, err := os.ReadFile(cmdPath); err == nil && len(cmdBytes) > 0 {
		// Remove trailing NUL if present
		if cmdBytes[len(cmdBytes)-1] == 0 {
			cmdBytes = cmdBytes[:len(cmdBytes)-1]
		}

		if len(cmdBytes) > 0 {
			args := bytes.Split(cmdBytes, []byte{0})
			strArgs := make([]string, len(args))
			for i, arg := range args {
				strArgs[i] = string(arg)
			}

			// Lossless JSON encoding
			if b, err := json.Marshal(strArgs); err == nil {
				s := string(b)
				inst.CommandLine = &s
			}
		}
	}

	// 4. Optional Metadata: User
	statusPath := filepath.Join(pidDir, "status")
	if statusBytes, err := os.ReadFile(statusPath); err == nil {
		if uidStr, found := extractUidFromStatus(string(statusBytes)); found {
			username := a.resolveUser(uidStr)
			inst.User = &username
		}
	}

	return inst
}

func (a *linuxAdapter) parseStat(pid int, statStr string) (*Instance, bool) {
	// The comm field is in parentheses and can contain spaces and parentheses.
	// We find the first '(' and the last ')'.
	firstParen := strings.IndexByte(statStr, '(')
	lastParen := strings.LastIndexByte(statStr, ')')
	if firstParen == -1 || lastParen == -1 || firstParen >= lastParen {
		return nil, false
	}

	name := statStr[firstParen+1 : lastParen]

	// The rest of the string after ") " contains the remaining fields
	if lastParen+2 >= len(statStr) {
		return nil, false
	}

	rest := statStr[lastParen+2:]
	fields := strings.Fields(rest)

	// 'rest' fields are:
	// index 0 = state
	// index 1 = ppid
	// ...
	// index 19 = starttime (22nd field overall, minus 1 for pid, minus 1 for comm)
	if len(fields) < 20 {
		return nil, false
	}

	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, false
	}

	startTimeTicks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return nil, false
	}

	// Calculate StartTime using bootTime and userHz
	// startTime = bootTime + (startTimeTicks / userHz)
	sec := a.bootTime + (startTimeTicks / a.userHz)
	nsec := ((startTimeTicks % a.userHz) * 1_000_000_000) / a.userHz

	start := time.Unix(sec, nsec)

	inst := &Instance{
		PID:       pid,
		StartTime: start,
		Name:      &name,
	}

	if ppid != 0 {
		inst.ParentPID = &ppid
	}

	return inst, true
}

func (a *linuxAdapter) resolveUser(uid string) string {
	if cached, exists := a.uidCache[uid]; exists {
		return cached
	}

	u, err := user.LookupId(uid)
	var username string
	if err != nil {
		// Fallback to uid string if lookup fails
		username = uid
	} else {
		username = u.Username
	}

	a.uidCache[uid] = username
	return username
}

func extractUidFromStatus(status string) (string, bool) {
	lines := strings.Split(status, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				// fields[0] is "Uid:", fields[1] is real UID
				return fields[1], true
			}
		}
	}
	return "", false
}

func getBootTime(procPath string) (int64, error) {
	statPath := filepath.Join(procPath, "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "btime ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return strconv.ParseInt(fields[1], 10, 64)
			}
		}
	}

	return 0, fmt.Errorf("btime not found in %s", statPath)
}

// getClkTick reads /proc/self/auxv to find the AT_CLKTCK value.
// AT_CLKTCK is type 17.
func getClkTick(procPath string) (int64, error) {
	auxvPath := filepath.Join(procPath, "self", "auxv")
	data, err := os.ReadFile(auxvPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read auxv: %w", err)
	}

	// auxv is an array of unsigned long pairs (type, value)
	// On 64-bit systems, these are 8 bytes each. On 32-bit, 4 bytes each.
	// We'll determine the size by checking if the length is a multiple of 16.
	// A more robust way is to check the size of int or pointer, but this is a heuristic.
	// To be safer without CGO, we can try 8-byte parsing, if that yields reasonable types,
	// use it. Otherwise, try 4-byte. Or we can just use the pointer size from runtime.

	// Assuming 64-bit for now, as most modern systems are. If it fails, fallback to 100.
	// But let's check pointer size.
	wordSize := 8
	// In Go, we can check pointer size:
	if strconv.IntSize == 32 {
		wordSize = 4
	}

	for i := 0; i+2*wordSize <= len(data); i += 2 * wordSize {
		var typ uint64
		var val uint64
		if wordSize == 8 {
			typ = binary.LittleEndian.Uint64(data[i : i+8])
			val = binary.LittleEndian.Uint64(data[i+8 : i+16])
		} else {
			typ = uint64(binary.LittleEndian.Uint32(data[i : i+4]))
			val = uint64(binary.LittleEndian.Uint32(data[i+4 : i+8]))
		}

		if typ == 17 { // AT_CLKTCK
			return int64(val), nil
		}
		if typ == 0 { // AT_NULL
			break
		}
	}

	return 0, fmt.Errorf("AT_CLKTCK not found in auxv")
}
