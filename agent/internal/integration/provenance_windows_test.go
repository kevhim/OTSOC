//go:build windows

package integration

import (
	"syscall"
	"testing"
)

// makeUnreadableFileForTesting creates an exclusively locked file using Windows CreateFile with shareMode=0.
// Any attempt by os.ReadFile to open this file in any goroutine will fail with ERROR_SHARING_VIOLATION.
func makeUnreadableFileForTesting(t *testing.T, path string) func() {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("failed to get utf16 ptr: %v", err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.CREATE_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("failed to create locked file: %v", err)
	}
	return func() {
		_ = syscall.CloseHandle(h)
	}
}
