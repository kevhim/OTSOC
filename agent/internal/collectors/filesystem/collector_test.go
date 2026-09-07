package filesystem

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

func TestFilesystemCollector_Integration(t *testing.T) {
	// Create a temporary directory to monitor
	tmpDir := t.TempDir()

	cfg := &config.Config{
		TenantID:     "tenant-1",
		SiteID:       "site-1",
		MonitorPaths: []string{tmpDir},
	}

	collector := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 100)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := collector.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}
	defer collector.Stop()

	// Give it a moment to start watching
	time.Sleep(100 * time.Millisecond)

	// Action 1: Create a file
	testFile := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(testFile, []byte("hello"), 0644)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	// Action 2: Modify a file
	err = os.WriteFile(testFile, []byte("hello world"), 0644)
	if err != nil {
		t.Fatalf("Failed to modify file: %v", err)
	}

	// Action 3: Rename a file
	renamedFile := filepath.Join(tmpDir, "test_renamed.txt")
	err = os.Rename(testFile, renamedFile)
	if err != nil {
		t.Fatalf("Failed to rename file: %v", err)
	}

	// Action 4: Delete a file
	err = os.Remove(renamedFile)
	if err != nil {
		t.Fatalf("Failed to remove file: %v", err)
	}

	// Collect events with a timeout
	collected := make(map[string]int)

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

loop:
	for {
		select {
		case ev := <-out:
			if ev.Category != "filesystem" {
				t.Errorf("Expected category 'filesystem', got %s", ev.Category)
			}
			if ev.TenantID != cfg.TenantID || ev.SiteID != cfg.SiteID {
				t.Errorf("Tenant/Site mismatch")
			}
			collected[ev.Action]++
			if collected["FILE_CREATE"] >= 1 &&
				collected["FILE_MODIFY"] >= 1 &&
				collected["FILE_RENAME"] >= 1 &&
				collected["FILE_DELETE"] >= 1 {
				// Got all expected event types
				break loop
			}
		case <-timer.C:
			t.Logf("Timeout waiting for events. Collected so far: %v", collected)
			break loop
		}
	}

	if collected["FILE_CREATE"] < 1 {
		t.Errorf("Missing FILE_CREATE event")
	}
	if collected["FILE_MODIFY"] < 1 {
		t.Errorf("Missing FILE_MODIFY event")
	}
	if collected["FILE_RENAME"] < 1 {
		t.Errorf("Missing FILE_RENAME event")
	}
	if collected["FILE_DELETE"] < 1 {
		t.Errorf("Missing FILE_DELETE event")
	}
}

func TestFilesystemCollector_FailuresAndMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Inaccessible / Missing Path Test
	missingPath := filepath.Join(tmpDir, "does_not_exist")
	cfg := &config.Config{
		TenantID:     "tenant-1",
		SiteID:       "site-1",
		MonitorPaths: []string{missingPath, tmpDir}, // valid path alongside invalid
	}

	collector := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic or fail start, just gracefully skip missingPath
	if err := collector.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}
	defer collector.Stop()
	time.Sleep(100 * time.Millisecond)

	// 2. Size = 0 Metadata Test
	zeroFile := filepath.Join(tmpDir, "zero.txt")
	os.WriteFile(zeroFile, []byte(""), 0644)

	// 3. Metadata Unavailable Test (Rapid delete before stat)
	// fsnotify might emit CREATE, but we delete it so fast stat fails.
	// Since this is a race condition, we'll try it a few times if we don't get it.
	go func() {
		for i := 0; i < 5; i++ {
			rapidFile := filepath.Join(tmpDir, fmt.Sprintf("rapid_%d.txt", i))
			os.WriteFile(rapidFile, []byte("rapid"), 0644)
			os.Remove(rapidFile)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	foundZeroSize := false
	foundMissingMetadata := false

loop:
	for {
		select {
		case ev := <-out:
			if ev.Metadata["file_path"] == zeroFile && ev.Action == "FILE_CREATE" {
				if sizeVal, ok := ev.Metadata["size"]; ok {
					var s float64
					switch v := sizeVal.(type) {
					case int64:
						s = float64(v)
					case float64:
						s = v
					case int:
						s = float64(v)
					}
					if s == 0 {
						foundZeroSize = true
					}
				}
			}

			if strings.Contains(ev.Metadata["file_path"].(string), "rapid_") {
				t.Logf("Received rapidFile event: Action=%s, Metadata=%v", ev.Action, ev.Metadata)
				if ev.Action == "FILE_CREATE" || ev.Action == "FILE_MODIFY" {
					if _, ok := ev.Metadata["size"]; !ok {
						foundMissingMetadata = true
					}
				}
			}

			if foundZeroSize && foundMissingMetadata {
				break loop
			}
		case <-timer.C:
			t.Logf("Timeout waiting for specific failure events")
			break loop
		}
	}

	if !foundZeroSize {
		t.Errorf("Did not properly emit size=0 metadata")
	}
	if !foundMissingMetadata {
		t.Errorf("Did not properly handle missing metadata (stat failure)")
	}
}

func TestFilesystemCollector_ShutdownWhileBlocked(t *testing.T) {
	// Create an unbuffered channel to guarantee blocking
	out := make(chan *events.CanonicalEvent)

	cfg := &config.Config{
		MonitorPaths: []string{t.TempDir()},
	}

	col := NewCollector(cfg)
	ctx := context.Background()

	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("Failed to start collector: %v", err)
	}

	// Create a file to trigger an event that will block on the unbuffered out channel
	testFile := filepath.Join(cfg.MonitorPaths[0], "block_test.txt")
	os.WriteFile(testFile, []byte("block"), 0644)

	// Wait a moment to ensure the collector is blocked in its send loop
	time.Sleep(1500 * time.Millisecond) // Wait >1s to also trigger the health log path

	// Now stop the collector while the channel is still blocked
	stopDone := make(chan struct{})
	go func() {
		col.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Success, Stop() returned
	case <-time.After(2 * time.Second):
		t.Fatalf("Collector Stop() hung indefinitely while output channel was blocked")
	}
}
