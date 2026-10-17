package governor

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockOSProvider struct {
	cpu    ValidFloat64
	mem    ValidUint64
	stUsed ValidUint64
	stRate ValidFloat64

	err error
	ts  time.Time
}

func (m *mockOSProvider) SampleCPU(ctx context.Context) (ValidFloat64, time.Time, error) {
	select {
	case <-ctx.Done():
		return ValidFloat64{}, time.Time{}, ctx.Err()
	default:
	}
	return m.cpu, m.ts, m.err
}

func (m *mockOSProvider) SampleMemory(ctx context.Context) (ValidUint64, time.Time, error) {
	select {
	case <-ctx.Done():
		return ValidUint64{}, time.Time{}, ctx.Err()
	default:
	}
	return m.mem, m.ts, m.err
}

func (m *mockOSProvider) SampleStorage(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
	select {
	case <-ctx.Done():
		return ValidUint64{}, ValidFloat64{}, time.Time{}, ctx.Err()
	default:
	}
	return m.stUsed, m.stRate, m.ts, m.err
}

func TestOSProvider_ValidNonZero(t *testing.T) {
	ts := time.Now()
	p := &mockOSProvider{
		cpu: ValidFloat64{Valid: true, Value: 10.5},
		ts:  ts,
	}
	cpu, sampledAt, err := p.SampleCPU(context.Background())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !cpu.Valid || cpu.Value != 10.5 {
		t.Fatalf("expected valid 10.5, got %v", cpu)
	}
	if !sampledAt.Equal(ts) {
		t.Fatalf("expected timestamp %v, got %v", ts, sampledAt)
	}
}

func TestOSProvider_ValidZero(t *testing.T) {
	ts := time.Now()
	p := &mockOSProvider{
		cpu: ValidFloat64{Valid: true, Value: 0.0},
		ts:  ts,
	}
	cpu, sampledAt, err := p.SampleCPU(context.Background())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !cpu.Valid || cpu.Value != 0.0 {
		t.Fatalf("expected valid 0.0, got %v", cpu)
	}
	if !sampledAt.Equal(ts) {
		t.Fatalf("expected timestamp %v, got %v", ts, sampledAt)
	}
}

func TestOSProvider_Unavailable(t *testing.T) {
	ts := time.Now()
	p := &mockOSProvider{
		cpu: ValidFloat64{Valid: false, Value: 0.0}, // Value MUST NOT be interpreted
		ts:  ts,
	}
	cpu, _, err := p.SampleCPU(context.Background())
	if err != nil {
		t.Fatalf("expected nil error for unavailable measurement, got %v", err)
	}
	if cpu.Valid {
		t.Fatalf("expected invalid/unavailable measurement, got %v", cpu)
	}
}

func TestOSProvider_Error(t *testing.T) {
	expectedErr := errors.New("read failed")
	p := &mockOSProvider{
		err: expectedErr,
	}
	_, _, err := p.SampleCPU(context.Background())
	if err == nil || err.Error() != expectedErr.Error() {
		t.Fatalf("expected specific error, got %v", err)
	}
}

func TestOSProvider_Storage_TimestampConsistency(t *testing.T) {
	ts := time.Now()
	p := &mockOSProvider{
		stUsed: ValidUint64{Valid: true, Value: 1024},
		stRate: ValidFloat64{Valid: true, Value: 256.0},
		ts:     ts,
	}
	used, rate, sampledAt, err := p.SampleStorage(context.Background())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !used.Valid || used.Value != 1024 {
		t.Fatalf("expected valid used 1024, got %v", used)
	}
	if !rate.Valid || rate.Value != 256.0 {
		t.Fatalf("expected valid rate 256.0, got %v", rate)
	}
	if !sampledAt.Equal(ts) {
		t.Fatalf("expected sampledAt %v, got %v", ts, sampledAt)
	}
}

func TestOSProvider_ContextCancellation(t *testing.T) {
	p := &mockOSProvider{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	_, _, err := p.SampleCPU(ctx)
	if err == nil || err != context.Canceled {
		t.Fatalf("expected context.Canceled error, got %v", err)
	}

	_, _, err = p.SampleMemory(ctx)
	if err == nil || err != context.Canceled {
		t.Fatalf("expected context.Canceled error, got %v", err)
	}

	_, _, _, err = p.SampleStorage(ctx)
	if err == nil || err != context.Canceled {
		t.Fatalf("expected context.Canceled error, got %v", err)
	}
}
