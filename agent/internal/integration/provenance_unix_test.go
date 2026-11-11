//go:build !windows

package integration

import (
	"os"
	"testing"
)

// makeUnreadableFileForTesting creates an entry that deterministically causes os.ReadFile to fail,
// even when tests are executed with root/superuser privileges (where chmod 0000 is bypassed by DAC).
// A broken symlink causes os.ReadFile/open to fail with ENOENT for all users, including root.
func makeUnreadableFileForTesting(t *testing.T, path string) func() {
	t.Helper()
	target := "/nonexistent/ot_soc_failure_target"
	if err := os.Symlink(target, path); err != nil {
		// Fallback for filesystems without symlink support
		if err := os.WriteFile(path, []byte("unreadable"), 0000); err != nil {
			t.Fatalf("failed to create unreadable test file: %v", err)
		}
		return func() {
			_ = os.Chmod(path, 0644)
			_ = os.Remove(path)
		}
	}
	return func() {
		_ = os.Remove(path)
	}
}
