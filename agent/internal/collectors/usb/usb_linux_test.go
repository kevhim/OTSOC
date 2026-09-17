//go:build linux
// +build linux

package usb

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseUevent(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantOk     bool
		wantAction string
		wantVID    string
		wantPID    string
		wantSerial string
		wantPath   string
	}{
		{
			name: "Valid USB Add",
			payload: "add@/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
				"ACTION=add\x00" +
				"DEVPATH=/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
				"SUBSYSTEM=usb\x00" +
				"DEVTYPE=usb_device\x00" +
				"PRODUCT=1d6b/0002/0532\x00" +
				"SERIAL=0000:00:14.0\x00",
			wantOk:     true,
			wantAction: "USB_INSERT",
			wantVID:    "1d6b",
			wantPID:    "0002",
			wantSerial: "0000:00:14.0",
			wantPath:   "/devices/pci0000:00/0000:00:14.0/usb1/1-1",
		},
		{
			name: "Valid USB Remove",
			payload: "remove@/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
				"ACTION=remove\x00" +
				"DEVPATH=/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
				"SUBSYSTEM=usb\x00" +
				"DEVTYPE=usb_device\x00" +
				"PRODUCT=1d6b/0002/0532\x00",
			wantOk:     true,
			wantAction: "USB_REMOVE",
			wantVID:    "1d6b",
			wantPID:    "0002",
			wantSerial: "",
			wantPath:   "/devices/pci0000:00/0000:00:14.0/usb1/1-1",
		},
		{
			name: "Non-USB payload",
			payload: "add@/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
				"ACTION=add\x00" +
				"SUBSYSTEM=pci\x00",
			wantOk: false,
		},
		{
			name: "USB interface payload",
			payload: "add@/devices/pci0000:00/0000:00:14.0/usb1/1-1/1-1:1.0\x00" +
				"ACTION=add\x00" +
				"SUBSYSTEM=usb\x00" +
				"DEVTYPE=usb_interface\x00" +
				"PRODUCT=1d6b/0002/0532\x00",
			wantOk: false, // We only want usb_device
		},
		{
			name: "Missing PRODUCT and SERIAL",
			payload: "add@/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
				"ACTION=add\x00" +
				"SUBSYSTEM=usb\x00" +
				"DEVTYPE=usb_device\x00",
			wantOk:     true,
			wantAction: "USB_INSERT",
			wantVID:    "",
			wantPID:    "",
			wantSerial: "",
			wantPath:   "",
		},
		{
			name: "Malformed payload with missing values",
			payload: "ACTION=\x00" +
				"SUBSYSTEM=usb\x00" +
				"DEVTYPE=usb_device\x00" +
				"PRODUCT=1d6b\x00", // Not enough parts for VID/PID
			wantOk: false, // Because ACTION is empty, it won't match "add" or "remove"
		},
		{
			name: "Malformed PRODUCT extraction",
			payload: "add@/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
				"ACTION=add\x00" +
				"SUBSYSTEM=usb\x00" +
				"DEVTYPE=usb_device\x00" +
				"PRODUCT=1d6b\x00", // No PID
			wantOk:     true,
			wantAction: "USB_INSERT",
			wantVID:    "", // Won't parse correctly, leaves blank
			wantPID:    "",
		},
		{
			name: "Short VID/PID zero padding",
			payload: "add@/devices/pci0000:00/0000:00:14.0/usb1/1-1\x00" +
				"ACTION=add\x00" +
				"SUBSYSTEM=usb\x00" +
				"DEVTYPE=usb_device\x00" +
				"PRODUCT=1a/b2/0532\x00",
			wantOk:     true,
			wantAction: "USB_INSERT",
			wantVID:    "001a",
			wantPID:    "00b2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, ok := parseUevent([]byte(tt.payload))
			if ok != tt.wantOk {
				t.Fatalf("Expected ok=%v, got %v", tt.wantOk, ok)
			}
			if !ok {
				return
			}
			if ev.Action != tt.wantAction {
				t.Errorf("Expected Action %s, got %s", tt.wantAction, ev.Action)
			}
			if ev.VendorID != tt.wantVID {
				t.Errorf("Expected VendorID %s, got %s", tt.wantVID, ev.VendorID)
			}
			if ev.ProductID != tt.wantPID {
				t.Errorf("Expected ProductID %s, got %s", tt.wantPID, ev.ProductID)
			}
			if ev.SerialNumber != tt.wantSerial {
				t.Errorf("Expected SerialNumber %s, got %s", tt.wantSerial, ev.SerialNumber)
			}
			if ev.DevicePath != tt.wantPath {
				t.Errorf("Expected DevicePath %s, got %s", tt.wantPath, ev.DevicePath)
			}
			if ev.OccurredAt.IsZero() {
				t.Errorf("Expected OccurredAt to be set")
			}
		})
	}
}

func TestDefaultStartOSWatcher_Lifecycle(t *testing.T) {
	out := make(chan USBEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())

	// Test graceful cancellation
	errCh := make(chan error, 1)
	go func() {
		errCh <- defaultStartOSWatcher(ctx, out)
	}()

	// Give it a moment to potentially hit the socket bind
	time.Sleep(100 * time.Millisecond)

	// Cancel context to trigger shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			// In some test environments (e.g. unprivileged containers), creating a raw netlink socket fails.
			// The contract requires error observability. We just verify it didn't hang.
			if strings.Contains(err.Error(), "operation not permitted") ||
				strings.Contains(err.Error(), "permission denied") ||
				strings.Contains(err.Error(), "address family not supported by protocol") {
				t.Logf("Socket creation failed gracefully due to env limitations: %v", err)
			} else {
				t.Logf("defaultStartOSWatcher exited with error: %v", err)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("defaultStartOSWatcher did not exit within timeout after context cancellation")
	}
}
