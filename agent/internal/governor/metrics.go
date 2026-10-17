package governor

import (
	"errors"
	"math"
	"sync"
	"time"
)

type EventPriority string

const (
	PriorityP0 EventPriority = "P0"
	PriorityP1 EventPriority = "P1"
	PriorityP2 EventPriority = "P2"
	PriorityP3 EventPriority = "P3"
	PriorityP4 EventPriority = "P4"
)

type DropReason string

const (
	ReasonQueueFull    DropReason = "QUEUE_FULL"
	ReasonStorageLimit DropReason = "STORAGE_LIMIT"
	ReasonEmergency    DropReason = "EMERGENCY"
)

type EventSource string

const (
	SourceNetworkCollector EventSource = "network"
	SourceUSBCollector     EventSource = "usb"
	SourceYaraScanner      EventSource = "yara"
	SourceSystem           EventSource = "system"
)

type EventCategory struct {
	Priority EventPriority
	Reason   DropReason
	Source   EventSource
}

type MetricsSnapshot struct {
	AgentCPUPercent            ValidFloat64
	AgentRSSBytes              ValidUint64
	QueueDepthCount            ValidUint64
	QueueOldestEventAgeSeconds ValidFloat64
	QueueSaturationPercent     ValidFloat64
	QueueCapacityCount         ValidUint64
	DiskStorageUsedBytes       ValidUint64
	// DiskWritePressureBytesSec represents the agent process's observed write-I/O transfer rate
	// in bytes per second.
	// Naming note: This field retains the name DiskWritePressureBytesSec for API/schema compatibility,
	// but it measures the agent process's observed write-I/O rate, NOT physical disk saturation or host disk pressure.
	DiskWritePressureBytesSec ValidFloat64

	EventsAggregatedTotal map[EventCategory]uint64
	EventsDroppedTotal    map[EventCategory]uint64

	GovernorState              HostResourceState
	TotalStateTransitions      uint64
	TimeInEmergencyModeSeconds float64
	LastTransitionTimestamp    time.Time
}

type GovernorMetrics struct {
	mu sync.Mutex

	agentCPUPercent ValidFloat64
	agentRSSBytes   ValidUint64

	queueDepthCount        ValidUint64
	queueOldestEventAgeSec ValidFloat64
	queueSaturationPercent ValidFloat64
	queueCapacityCount     ValidUint64

	diskStorageUsedBytes         ValidUint64
	diskWritePressureBytesPerSec ValidFloat64

	eventsAggregatedTotal map[EventCategory]uint64
	eventsDroppedTotal    map[EventCategory]uint64

	currentState            HostResourceState
	totalStateTransitions   uint64
	currentEmergencyStart   time.Time
	lastTransitionTimestamp time.Time
	nowFunc                 NowFunc
}

func NewGovernorMetrics(now NowFunc) *GovernorMetrics {
	if now == nil {
		now = time.Now
	}
	return &GovernorMetrics{
		eventsAggregatedTotal: make(map[EventCategory]uint64),
		eventsDroppedTotal:    make(map[EventCategory]uint64),
		currentState:          StateNormal,
		nowFunc:               now,
	}
}

func (m *GovernorMetrics) UpdateCPU(v ValidFloat64) error {
	if v.Valid {
		if math.IsNaN(v.Value) || v.Value < 0 || v.Value > 100 {
			return errors.New("invalid CPU measurement")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentCPUPercent = v
	return nil
}

func (m *GovernorMetrics) UpdateMemory(v ValidUint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentRSSBytes = v
}

func (m *GovernorMetrics) UpdateQueue(depth, capacity ValidUint64, oldestAge ValidFloat64) error {
	if depth.Valid && capacity.Valid {
		if depth.Value > capacity.Value {
			return errors.New("depth cannot exceed capacity")
		}
	}
	if capacity.Valid && capacity.Value == 0 {
		return errors.New("capacity must be > 0 for saturation calculation")
	}
	if oldestAge.Valid && (math.IsNaN(oldestAge.Value) || oldestAge.Value < 0) {
		return errors.New("invalid oldest age")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.queueDepthCount = depth
	m.queueCapacityCount = capacity
	m.queueOldestEventAgeSec = oldestAge
	if depth.Valid && capacity.Valid {
		m.queueSaturationPercent = ValidFloat64{Valid: true, Value: float64(depth.Value) / float64(capacity.Value)}
	} else {
		m.queueSaturationPercent = ValidFloat64{Valid: false}
	}
	return nil
}

// UpdateDisk updates the agent persistence footprint and process write-I/O rate.
// writePressure represents the process observed write-I/O transfer rate in bytes per second,
// not physical disk throughput or host disk saturation.
func (m *GovernorMetrics) UpdateDisk(used ValidUint64, writePressure ValidFloat64) error {
	if writePressure.Valid && (math.IsNaN(writePressure.Value) || writePressure.Value < 0) {
		return errors.New("invalid disk write pressure")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diskStorageUsedBytes = used
	m.diskWritePressureBytesPerSec = writePressure
	return nil
}

func (m *GovernorMetrics) RecordTransition(tr Transition) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.currentState = tr.NewState
	m.totalStateTransitions++
	m.lastTransitionTimestamp = tr.ObservedAt

	if tr.NewState == StateEmergency {
		if m.currentEmergencyStart.IsZero() {
			m.currentEmergencyStart = tr.ObservedAt
		}
	} else {
		m.currentEmergencyStart = time.Time{}
	}
}

func (m *GovernorMetrics) IncrementAggregated(cat EventCategory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventsAggregatedTotal[cat]++
}

func (m *GovernorMetrics) IncrementDropped(cat EventCategory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventsDroppedTotal[cat]++
}

func (m *GovernorMetrics) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.nowFunc()

	snap := MetricsSnapshot{
		AgentCPUPercent:            m.agentCPUPercent,
		AgentRSSBytes:              m.agentRSSBytes,
		QueueDepthCount:            m.queueDepthCount,
		QueueOldestEventAgeSeconds: m.queueOldestEventAgeSec,
		QueueSaturationPercent:     m.queueSaturationPercent,
		QueueCapacityCount:         m.queueCapacityCount,
		DiskStorageUsedBytes:       m.diskStorageUsedBytes,
		DiskWritePressureBytesSec:  m.diskWritePressureBytesPerSec,
		GovernorState:              m.currentState,
		TotalStateTransitions:      m.totalStateTransitions,
		LastTransitionTimestamp:    m.lastTransitionTimestamp,
	}

	snap.EventsAggregatedTotal = make(map[EventCategory]uint64, len(m.eventsAggregatedTotal))
	for k, v := range m.eventsAggregatedTotal {
		snap.EventsAggregatedTotal[k] = v
	}

	snap.EventsDroppedTotal = make(map[EventCategory]uint64, len(m.eventsDroppedTotal))
	for k, v := range m.eventsDroppedTotal {
		snap.EventsDroppedTotal[k] = v
	}

	if !m.currentEmergencyStart.IsZero() && snap.GovernorState == StateEmergency {
		snap.TimeInEmergencyModeSeconds = now.Sub(m.currentEmergencyStart).Seconds()
	} else {
		snap.TimeInEmergencyModeSeconds = 0.0
	}

	return snap
}
