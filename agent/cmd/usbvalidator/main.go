package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"redcyberfox/agent/internal/collectors/usb"
	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"
)

func main() {
	verbose := flag.Bool("verbose", false, "Print sensitive metadata like serial_number and device_path")
	flag.Parse()

	fmt.Println("=====================================================")
	fmt.Println("Phase 2E.3.1 Windows USB Real-Host Validator")
	fmt.Println("=====================================================")
	fmt.Println("Instructions:")
	fmt.Println("1. Plug in a USB device (e.g. flash drive, mouse, keyboard).")
	fmt.Println("2. Observe the USB_INSERT event.")
	fmt.Println("3. Unplug the device.")
	fmt.Println("4. Observe the USB_REMOVE event.")
	fmt.Println("5. Press Ctrl+C to exit and verify clean shutdown.")
	fmt.Println("=====================================================")

	cfg := &config.Config{
		TenantID: "validator-tenant",
		SiteID:   "validator-site",
	}

	usbCol := usb.NewCollector(cfg)
	out := make(chan *events.CanonicalEvent, 100)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nReceived interrupt, shutting down collector...")
		cancel()
	}()

	if err := usbCol.Start(ctx, out); err != nil {
		log.Fatalf("Failed to start USB collector: %v", err)
	}

	// Read events until shutdown
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				return
			}
			printEvent(ev, *verbose)
		case <-ctx.Done():
			usbCol.Stop()
			fmt.Println("Shutdown complete.")
			return
		}
	}
}

func printEvent(ev *events.CanonicalEvent, verbose bool) {
	fmt.Printf("\n--- Captured Event ---\n")
	fmt.Printf("Category:    %s\n", ev.Category)
	fmt.Printf("Action:      %s\n", ev.Action)
	fmt.Printf("Event ID:    %s\n", ev.EventID)
	fmt.Printf("Occurred At: %s\n", ev.OccurredAt.String())

	if vid, ok := ev.Metadata["vendor_id"]; ok {
		fmt.Printf("Vendor ID:   %s\n", vid)
	}
	if pid, ok := ev.Metadata["product_id"]; ok {
		fmt.Printf("Product ID:  %s\n", pid)
	}

	if verbose {
		if serial, ok := ev.Metadata["serial_number"]; ok {
			fmt.Printf("Serial Num:  %s\n", serial)
		}
		if path, ok := ev.Metadata["device_path"]; ok {
			fmt.Printf("Device Path: %s\n", path)
		}

		rawJSON, _ := json.MarshalIndent(ev, "", "  ")
		fmt.Printf("Raw JSON:\n%s\n", string(rawJSON))
	} else {
		fmt.Println("(Sensitive fields like serial_number hidden. Use --verbose to view)")
	}
	fmt.Println("----------------------")
}
