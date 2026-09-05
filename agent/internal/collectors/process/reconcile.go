package process

// Snapshot represents a collection of normalized Process Instances
// captured at a specific point in time. It serves as the minimal
// contract between OS-specific process observation and the generic
// lifecycle state machine.
type Snapshot struct {
	Instances []*Instance
}
