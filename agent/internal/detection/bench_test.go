package detection

import (
	"context"
	"testing"
	"time"

	"redcyberfox/agent/internal/interfaces"
	"redcyberfox/pkg/events"
)

type noOpStorage struct {
	interfaces.Storage
}

func (n *noOpStorage) Store(ctx context.Context, ev *events.CanonicalEvent) error {
	return nil // no-op sink to measure detection throughput without unbounded slice growth
}

func BenchmarkDetectionEngine(b *testing.B) {
	store := &noOpStorage{}
	engine := NewEngine(store, []Rule{
		&TestIOCRule{},
		&RealIOCExecRule{},
	})

	ctx := context.Background()
	ev := &events.CanonicalEvent{
		EventID:       "bench-123",
		TenantID:      "tenant-bench",
		SiteID:        "site-bench",
		Source:        "linux_process",
		OccurredAt:    time.Now().UTC(),
		Category:      "process_start",
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"test_ioc":        "RF-TEST-MALICIOUS",
			"executable_path": "/opt/malicious/bin/miner",
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Because we're using synchronous evaluator, we just evaluate in loop
		engine.Evaluate(ctx, ev)
	}
}
