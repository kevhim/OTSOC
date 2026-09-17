//go:build linux
// +build linux

package usb

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// defaultStartOSWatcher subscribes to Linux kernel uevents via netlink.
// It parses the events and sends valid USB events to the out channel.
func defaultStartOSWatcher(ctx context.Context, out chan<- USBEvent) error {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return fmt.Errorf("failed to create netlink socket: %w", err)
	}
	defer unix.Close(fd)

	addr := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: 1, // kernel uevents
		Pid:    0, // kernel-assigned port ID
	}

	if err := unix.Bind(fd, addr); err != nil {
		return fmt.Errorf("failed to bind netlink socket: %w", err)
	}

	// Receive timeout: 1s
	// Purpose: bounded shutdown latency while using blocking netlink recv
	// Status: provisional, validated during Linux host testing
	tv := unix.NsecToTimeval(time.Second.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return fmt.Errorf("failed to set SO_RCVTIMEO: %w", err)
	}

	buf := make([]byte, 8192)

	for {
		// Check cancellation before blocking on Recvfrom
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				// Expected from receive timeout. Context is checked at the top of the loop.
				continue
			}
			// Fatal socket/read error
			return fmt.Errorf("netlink recvfrom failed: %w", err)
		}

		if n <= 0 || n > len(buf) {
			continue
		}

		if ev, ok := parseUevent(buf[:n]); ok {
			select {
			case out <- ev:
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// parseUevent parses a netlink uevent payload.
// A uevent payload consists of a header string followed by null-separated key=value pairs.
func parseUevent(payload []byte) (USBEvent, bool) {
	var ev USBEvent
	var subsystem, devtype, action string

	// Uevent fields are null-separated strings
	parts := bytes.Split(payload, []byte{0})

	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		
		strPart := string(part)
		idx := strings.IndexByte(strPart, '=')
		if idx == -1 {
			continue // usually the first header line without '=' or trailing empty string
		}

		key := strPart[:idx]
		val := strPart[idx+1:]

		switch key {
		case "ACTION":
			action = val
		case "SUBSYSTEM":
			subsystem = val
		case "DEVTYPE":
			devtype = val
		case "DEVPATH":
			ev.DevicePath = val
		case "PRODUCT":
			// Expected format: VID/PID/REV (hex). Defensive parsing.
			prodParts := strings.SplitN(val, "/", 3)
			if len(prodParts) >= 2 {
				// Zero-pad to 4 hex chars to maintain canonical VID/PID consistency if needed,
				// or just take raw string. We take the raw string but ensure lowercase if they are hex.
				ev.VendorID = strings.ToLower(prodParts[0])
				ev.ProductID = strings.ToLower(prodParts[1])
				// Ensure they are exactly 4 chars if they drop leading zeroes
				for len(ev.VendorID) > 0 && len(ev.VendorID) < 4 {
					ev.VendorID = "0" + ev.VendorID
				}
				for len(ev.ProductID) > 0 && len(ev.ProductID) < 4 {
					ev.ProductID = "0" + ev.ProductID
				}
			}
		case "SERIAL":
			ev.SerialNumber = val
		}
	}

	if subsystem != "usb" || devtype != "usb_device" {
		return ev, false
	}

	if action == "add" {
		ev.Action = "USB_INSERT"
	} else if action == "remove" {
		ev.Action = "USB_REMOVE"
	} else {
		return ev, false
	}

	// OccurredAt is recorded as the time we parse it (observation time).
	ev.OccurredAt = time.Now().UTC()

	return ev, true
}
