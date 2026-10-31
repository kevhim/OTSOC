package governor

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"redcyberfox/agent/internal/storage"
	"redcyberfox/pkg/events"
)

func TestSQLiteResourceProviderCountsOptionalSidecars(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	if err := os.WriteFile(dbPath, make([]byte, 100), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+"-wal", make([]byte, 20), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+"-shm", make([]byte, 10), 0600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := &sqliteResourceProvider{
		OSResourceProvider: &mockOSProvider{
			stUsed: ValidUint64{Value: 100, Valid: true},
			stRate: ValidFloat64{Value: 42, Valid: true}, ts: now,
		},
		dbPath: dbPath,
	}
	used, rate, sampledAt, err := p.SampleStorage(context.Background())
	if err != nil || !used.Valid || used.Value != 130 || !rate.Valid || rate.Value != 42 || !sampledAt.Equal(now) {
		t.Fatalf("unexpected full footprint: used=%v rate=%v sampledAt=%v err=%v", used, rate, sampledAt, err)
	}

	if err := os.Remove(dbPath + "-wal"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dbPath + "-shm"); err != nil {
		t.Fatal(err)
	}
	used, _, _, err = p.SampleStorage(context.Background())
	if err != nil || !used.Valid || used.Value != 100 {
		t.Fatalf("missing optional sidecars must not invalidate the DB footprint: used=%v err=%v", used, err)
	}
}

func TestSQLiteResourceProviderRealHostFootprint(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	if err := os.WriteFile(dbPath, make([]byte, 100), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+"-wal", make([]byte, 20), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := NewSQLiteResourceProvider(dbPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	used, _, _, err := p.SampleStorage(context.Background())
	if err != nil || !used.Valid || used.Value != 120 {
		t.Fatalf("real OS provider must include the SQLite WAL: used=%v err=%v", used, err)
	}
}

func TestSQLiteResourceProviderTracksLiveWALDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	db := storage.NewSQLiteStorage(dbPath, "", 50*1024*1024)
	if err := db.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Store(context.Background(), &events.CanonicalEvent{
		EventID: "resource-footprint-1", Severity: "INFO", Source: "resource-test",
	}); err != nil {
		t.Fatal(err)
	}

	provider, err := NewSQLiteResourceProvider(dbPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	used, _, _, err := provider.SampleStorage(context.Background())
	if err != nil || !used.Valid {
		t.Fatalf("live SQLite storage sample failed: used=%v err=%v", used, err)
	}

	var expected uint64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(dbPath + suffix)
		if err != nil {
			if os.IsNotExist(err) && suffix != "" {
				continue
			}
			t.Fatal(err)
		}
		expected += uint64(info.Size())
	}
	if used.Value != expected {
		t.Fatalf("live SQLite footprint differs from DB/WAL/SHM: sampled=%d actual=%d", used.Value, expected)
	}
}

func TestSQLiteResourceProviderRejectsInvalidSidecarAndOverflow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	if err := os.Mkdir(dbPath+"-wal", 0700); err != nil {
		t.Fatal(err)
	}
	base := &mockOSProvider{stUsed: ValidUint64{Value: 10, Valid: true}}
	p := &sqliteResourceProvider{OSResourceProvider: base, dbPath: dbPath}
	used, _, _, err := p.SampleStorage(context.Background())
	if err == nil || used.Valid {
		t.Fatalf("directory sidecar must be a sampling failure: used=%v err=%v", used, err)
	}

	if err := os.Remove(dbPath + "-wal"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath+"-wal", []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	base.stUsed.Value = math.MaxUint64
	used, _, _, err = p.SampleStorage(context.Background())
	if err == nil || used.Valid {
		t.Fatalf("overflow must be a sampling failure: used=%v err=%v", used, err)
	}
}
