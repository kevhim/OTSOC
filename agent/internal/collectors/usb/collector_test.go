package usb

import (
	"context"
	"testing"
	"time"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

func TestUSBCollector_InsertEvent(t *testing.T) {
	originalWatcher := startOSWatcher
	defer func() { startOSWatcher = originalWatcher }()

	startOSWatcher = func(ctx context.Context, out chan<- USBEvent) error {
		out <- USBEvent{
			Action:       "USB_INSERT",
			VendorID:     "1234",
			ProductID:    "5678",
			SerialNumber: "SN999",
			DevicePath:   "\\\\?\\USB#VID_1234&PID_5678#SN999#{guid}",
			OccurredAt:   time.Now().UTC(),
		}
		<-ctx.Done()
		return nil
	}

	cfg := &config.Config{TenantID: "t1", SiteID: "s1"}
	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 1)

	ctx := context.Background()
	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer col.Stop()

	select {
	case ev := <-out:
		if ev.Category != "usb" || ev.Action != "USB_INSERT" {
			t.Errorf("expected usb/USB_INSERT, got %s/%s", ev.Category, ev.Action)
		}
		if ev.Metadata["vendor_id"] != "1234" || ev.Metadata["product_id"] != "5678" || ev.Metadata["serial_number"] != "SN999" {
			t.Errorf("metadata missing or incorrect: %v", ev.Metadata)
		}
		if ev.EventID == "" {
			t.Errorf("missing event ID")
		}
		if ev.SeqNo != 0 {
			t.Errorf("seq_no should be 0")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for event")
	}
}

func TestUSBCollector_MissingMetadata(t *testing.T) {
	originalWatcher := startOSWatcher
	defer func() { startOSWatcher = originalWatcher }()

	startOSWatcher = func(ctx context.Context, out chan<- USBEvent) error {
		// Missing serial number and product id
		out <- USBEvent{
			Action:     "USB_REMOVE",
			VendorID:   "abcd",
			DevicePath: "\\\\?\\USB#VID_ABCD#{guid}",
			OccurredAt: time.Now().UTC(),
		}
		<-ctx.Done()
		return nil
	}

	cfg := &config.Config{TenantID: "t1"}
	col := NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 1)

	ctx := context.Background()
	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer col.Stop()

	select {
	case ev := <-out:
		if ev.Action != "USB_REMOVE" {
			t.Errorf("expected USB_REMOVE")
		}
		if ev.Metadata["vendor_id"] != "abcd" {
			t.Errorf("expected vendor_id abcd")
		}
		if _, ok := ev.Metadata["serial_number"]; ok {
			t.Errorf("expected no serial_number")
		}
		if _, ok := ev.Metadata["product_id"]; ok {
			t.Errorf("expected no product_id")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout")
	}
}

func TestUSBCollector_ShutdownWhileBlocked(t *testing.T) {
	originalWatcher := startOSWatcher
	defer func() { startOSWatcher = originalWatcher }()

	startOSWatcher = func(ctx context.Context, out chan<- USBEvent) error {
		out <- USBEvent{Action: "USB_INSERT"}
		<-ctx.Done()
		return nil
	}

	cfg := &config.Config{TenantID: "t1"}
	col := NewCollector(cfg)
	
	// Unbuffered channel, so the emitEvent timer will trigger after 1s
	out := make(chan *events.CanonicalEvent) 
	
	ctx := context.Background()
	if err := col.Start(ctx, out); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Give it time to hit the blocked send
	time.Sleep(100 * time.Millisecond)

	stopDone := make(chan struct{})
	go func() {
		col.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// success
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop hung")
	}
}

func TestParseDevicePath(t *testing.T) {
	tests := []struct {
		path       string
		wantVid    string
		wantPid    string
		wantSerial string
	}{
		{
			path:       `\\?\USB#VID_1234&PID_5678#SERIALNUMBER#{guid}`,
			wantVid:    "1234",
			wantPid:    "5678",
			wantSerial: "SERIALNUMBER",
		},
		{
			path:       `\\?\USB#VID_ABCD&PID_EF01#5&1A2B3C&0&1#{guid}`,
			wantVid:    "abcd",
			wantPid:    "ef01",
			wantSerial: "", // Instance ID with & is typically not a stable serial
		},
		{
			path:       `\\?\USB#UNKNOWN#1234#{guid}`,
			wantVid:    "",
			wantPid:    "",
			wantSerial: "1234",
		},
	}

	for _, tt := range tests {
		v, p, s := parseDevicePath(tt.path)
		if v != tt.wantVid || p != tt.wantPid || s != tt.wantSerial {
			t.Errorf("parseDevicePath(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.path, v, p, s, tt.wantVid, tt.wantPid, tt.wantSerial)
		}
	}
}
