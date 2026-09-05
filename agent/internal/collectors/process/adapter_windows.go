//go:build windows

package process

import (
	"context"
	"fmt"
	"log"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// osAPI defines a high-level testable interface for Windows process operations
type osAPI interface {
	// EnumerateProcesses returns a list of basic process info (PID, PPID, Name)
	EnumerateProcesses() ([]ProcessEntry, error)
	// GetProcessStartTime retrieves the exact creation time of a process
	GetProcessStartTime(pid uint32) (time.Time, error)
	// GetExecutablePath retrieves the full image name of a process
	GetExecutablePath(pid uint32) (string, error)
	// GetProcessUserSID retrieves the SID of the process user
	GetProcessUserSID(pid uint32) (string, error)
	// ResolveSID resolves a SID string to a username
	ResolveSID(sid string) (string, error)
}

// ProcessEntry contains the basic data returned by process enumeration
type ProcessEntry struct {
	PID  uint32
	PPID uint32
	Name string
}

type windowsAdapter struct {
	api osAPI
	// userCache stores SID-to-Username mapping for a single reconciliation pass
	userCache map[string]string
}

var defaultOSAPI osAPI = &realWindowsAPI{}

func (c *ProcessCollector) startOSAdapter(ctx context.Context) {
	interval := 30 * time.Second
	if c.cfg != nil && c.cfg.ProcessInterval != "" {
		if d, err := time.ParseDuration(c.cfg.ProcessInterval); err == nil && d > 0 {
			interval = d
		} else {
			log.Printf("ProcessCollector: invalid process_interval %q, defaulting to 30s", c.cfg.ProcessInterval)
		}
	}

	adapter := &windowsAdapter{
		api: defaultOSAPI,
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial reconciliation
	snapshot, err := adapter.captureSnapshot()
	if err != nil {
		log.Printf("ProcessCollector: initial Windows snapshot failed: %v", err)
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
				log.Printf("ProcessCollector: Windows snapshot failed: %v", err)
				continue // Do not call Reconcile if enumeration failed
			}
			c.Reconcile(ctx, snapshot)
		}
	}
}

func (a *windowsAdapter) captureSnapshot() (*Snapshot, error) {
	a.userCache = make(map[string]string) // Reset cache for this pass

	entries, err := a.api.EnumerateProcesses()
	if err != nil {
		return nil, fmt.Errorf("process enumeration failed: %w", err)
	}

	var instances []*Instance

	for _, entry := range entries {
		// Mandatory StartTime
		startTime, err := a.api.GetProcessStartTime(entry.PID)
		if err != nil {
			// Process may have exited, or access denied. Record as unobservable.
			instances = append(instances, &Instance{PID: int(entry.PID)})
			continue
		}

		inst := &Instance{
			PID:       int(entry.PID),
			StartTime: startTime,
		}

		// Name
		if entry.Name != "" {
			name := entry.Name
			inst.Name = &name
		}

		// PPID
		if entry.PPID != 0 {
			ppid := int(entry.PPID)
			inst.ParentPID = &ppid
		}

		// CommandLine is intentionally left unavailable in Phase 2C.3
		// due to the lack of a lightweight, safe native API.

		// Optional: ExecutablePath
		if path, err := a.api.GetExecutablePath(entry.PID); err == nil {
			inst.ExecutablePath = &path
		}

		// Optional: User
		if sid, err := a.api.GetProcessUserSID(entry.PID); err == nil {
			inst.User = a.resolveUser(sid)
		}

		instances = append(instances, inst)
	}

	return &Snapshot{
		Instances: instances,
	}, nil
}

func (a *windowsAdapter) resolveUser(sid string) *string {
	if cached, exists := a.userCache[sid]; exists {
		if cached == "" {
			return nil
		}
		return &cached
	}

	username, err := a.api.ResolveSID(sid)
	if err != nil {
		a.userCache[sid] = "" // Cache the failure as empty string
		return nil
	}

	a.userCache[sid] = username
	return &username
}

// --- Windows API implementation ---

var (
	sysCreateToolhelp32Snapshot = windows.CreateToolhelp32Snapshot
	sysProcess32First           = windows.Process32First
	sysProcess32Next            = windows.Process32Next
)

type realWindowsAPI struct{}

func (api *realWindowsAPI) EnumerateProcesses() ([]ProcessEntry, error) {
	// Create snapshot. We want all processes.
	handle, err := sysCreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle) // Explicit handle cleanup

	var entries []ProcessEntry
	var pe32 windows.ProcessEntry32
	pe32.Size = uint32(unsafe.Sizeof(pe32))

	err = sysProcess32First(handle, &pe32)
	if err != nil {
		return nil, err
	}

	for {
		name := windows.UTF16ToString(pe32.ExeFile[:])
		entries = append(entries, ProcessEntry{
			PID:  pe32.ProcessID,
			PPID: pe32.ParentProcessID,
			Name: name,
		})

		err = sysProcess32Next(handle, &pe32)
		if err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				break
			}
			return nil, err
		}
	}

	return entries, nil
}

func (api *realWindowsAPI) GetProcessStartTime(pid uint32) (time.Time, error) {

	// Least privilege access right
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return time.Time{}, err
	}
	defer windows.CloseHandle(handle)

	var creationTime, exitTime, kernelTime, userTime windows.Filetime
	err = windows.GetProcessTimes(handle, &creationTime, &exitTime, &kernelTime, &userTime)
	if err != nil {
		return time.Time{}, err
	}

	return filetimeToTime(creationTime)
}

func (api *realWindowsAPI) GetExecutablePath(pid uint32) (string, error) {

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	buf := make([]uint16, windows.MAX_PATH)
	for {
		size := uint32(len(buf))
		err = windows.QueryFullProcessImageName(handle, 0, &buf[0], &size)
		if err == nil {
			return windows.UTF16ToString(buf[:size]), nil
		}
		if err == windows.ERROR_INSUFFICIENT_BUFFER {
			buf = make([]uint16, len(buf)*2)
			continue
		}
		return "", err
	}
}

func (api *realWindowsAPI) GetProcessUserSID(pid uint32) (string, error) {

	processHandle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(processHandle)

	var tokenHandle windows.Token
	err = windows.OpenProcessToken(processHandle, windows.TOKEN_QUERY, &tokenHandle)
	if err != nil {
		return "", err
	}
	defer tokenHandle.Close()

	tokenUser, err := tokenHandle.GetTokenUser()
	if err != nil {
		return "", err
	}

	return tokenUser.User.Sid.String(), nil
}

func (api *realWindowsAPI) ResolveSID(sidStr string) (string, error) {
	sid, err := windows.StringToSid(sidStr)
	if err != nil {
		return "", err
	}

	account, domain, _, err := sid.LookupAccount("")
	if err != nil {
		return "", err
	}

	if domain != "" {
		return domain + "\\" + account, nil
	}
	return account, nil
}

// filetimeToTime converts a Windows FILETIME to a Go time.Time.
// A FILETIME contains a 64-bit value representing the number of
// 100-nanosecond intervals since January 1, 1601 (UTC).
func filetimeToTime(ft windows.Filetime) (time.Time, error) {
	if ft.HighDateTime == 0 && ft.LowDateTime == 0 {
		return time.Time{}, fmt.Errorf("zero filetime")
	}

	t := time.Unix(0, ft.Nanoseconds())
	return t, nil
}
