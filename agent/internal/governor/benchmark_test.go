package governor

import (
	"testing"
	"time"
)

// Benchmark the evaluation logic to ensure it's lightweight.
func BenchmarkGovernor_Evaluate(b *testing.B) {
	now := time.Now()
	gov, _ := NewGovernor(defaultTestConfig(), func() time.Time { return now }, nil)

	sig := ResourceSignals{
		CPUUtilizationPercent: 50.0,
		MemoryRSSBytes:        100 * 1024 * 1024,
		StoragePressure:       0.5,
		QueueSaturation:       0.1,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		gov.Evaluate(sig)
	}
}
