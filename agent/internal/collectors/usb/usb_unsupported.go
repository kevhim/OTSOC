//go:build !windows
// +build !windows

package usb

import (
	"context"
	"fmt"
)

func defaultStartOSWatcher(ctx context.Context, out chan<- USBEvent) error {
	return fmt.Errorf("usb telemetry is not implemented on this platform")
}
