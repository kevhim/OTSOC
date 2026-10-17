package governor

import (
	"context"
	"fmt"
	"math"
	"os"
	"time"
)

// NewSQLiteResourceProvider measures the same DB, WAL, and SHM footprint used by
// the edge storage quota. WAL and SHM files are optional because SQLite creates
// and removes them during normal operation; the main database is required.
func NewSQLiteResourceProvider(dbPath string, now func() time.Time) (OSResourceProvider, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("SQLite database path is required")
	}
	base, err := NewOSResourceProvider([]string{dbPath}, now)
	if err != nil {
		return nil, err
	}
	return &sqliteResourceProvider{OSResourceProvider: base, dbPath: dbPath}, nil
}

type sqliteResourceProvider struct {
	OSResourceProvider
	dbPath string
}

func (p *sqliteResourceProvider) SampleStorage(ctx context.Context) (ValidUint64, ValidFloat64, time.Time, error) {
	used, writeRate, sampledAt, err := p.OSResourceProvider.SampleStorage(ctx)
	if err != nil || !used.Valid {
		return used, writeRate, sampledAt, err
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if err := ctx.Err(); err != nil {
			return ValidUint64{}, ValidFloat64{}, sampledAt, err
		}
		path := p.dbPath + suffix
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("stat SQLite sidecar %s: %w", path, err)
		}
		if !info.Mode().IsRegular() || info.Size() < 0 {
			return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("SQLite sidecar %s is not a regular file", path)
		}
		size := uint64(info.Size())
		if size > math.MaxUint64-used.Value {
			return ValidUint64{}, ValidFloat64{}, sampledAt, fmt.Errorf("SQLite footprint exceeds uint64")
		}
		used.Value += size
	}
	return used, writeRate, sampledAt, nil
}
