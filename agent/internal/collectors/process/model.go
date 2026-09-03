package process

import (
	"fmt"
	"time"
)

// Instance represents a normalized process instance observed on the system.
type Instance struct {
	// Required for strong instance identity (PID reuse safety)
	PID       int
	StartTime time.Time

	// Optional metadata
	Name           *string
	ParentPID      *int
	ExecutablePath *string
	CommandLine    *string
	User           *string
}

// IdentityKey returns the strong identity for this process instance,
// derived from both PID and StartTime to safely handle PID reuse.
func (i *Instance) IdentityKey() string {
	return fmt.Sprintf("%d-%d", i.PID, i.StartTime.UnixNano())
}
