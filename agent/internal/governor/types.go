package governor

import "time"

// HostResourceState represents the current operating state of the agent host.
type HostResourceState string

const (
	StateNormal        HostResourceState = "NORMAL"
	StateResourceSaver HostResourceState = "RESOURCE-SAVER"
	StateEmergency     HostResourceState = "EMERGENCY"
)

// NetworkState represents the current connectivity state. Logically separate from HostResourceState.
type NetworkState string

const (
	NetworkOnline  NetworkState = "ONLINE"
	NetworkOffline NetworkState = "OFFLINE"
)

// ResourceSignals represents the latest measurements of system pressure.
type ResourceSignals struct {
	CPUUtilizationPercent float64
	MemoryRSSBytes        uint64
	StoragePressure       float64 // 0.0 to 1.0 representing budget used
	QueueSaturation       float64 // 0.0 to 1.0 representing fill ratio
	NetworkState          NetworkState
}

// Config defines the provisional thresholds for the governor.
type Config struct {
	// CPU (Percentage 0-100)
	CPUSaverThreshold     float64
	CPUEmergencyThreshold float64
	CPURecoveryMargin     float64

	// Memory (Bytes)
	MemorySaverBytes          uint64
	MemoryEmergencyBytes      uint64
	MemoryRecoveryMarginBytes uint64

	// Storage (Ratio 0.0-1.0)
	StorageSaverRatio          float64
	StorageEmergencyRatio      float64
	StorageRecoveryMarginRatio float64

	// Queue Saturation (Ratio 0.0-1.0)
	QueueSaverRatio          float64
	QueueEmergencyRatio      float64
	QueueRecoveryMarginRatio float64

	// Time required to remain below recovery threshold to transition down
	RecoveryDuration time.Duration
}
