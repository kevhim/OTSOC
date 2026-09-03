//go:build !linux

package process

import (
	"context"
	"log"
)

// startOSAdapter is a no-op on non-Linux platforms for Phase 2C.
// Future phases will implement OS-specific loops (e.g., Windows ETW).
func (c *ProcessCollector) startOSAdapter(ctx context.Context) {
	log.Println("Process collector OS adapter not implemented for this OS. Blocking.")
	<-ctx.Done()
}
