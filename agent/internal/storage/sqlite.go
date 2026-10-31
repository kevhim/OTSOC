package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"redcyberfox/agent/internal/identity"
	"redcyberfox/pkg/events"
)

var (
	ErrStoreFailedBeforeCommit = errors.New("store definitely failed before commit")
	ErrStoreUncertain          = errors.New("store commit result uncertain")
)

type SQLiteStorage struct {
	dbPath       string
	identityPath string
	quotaBytes   int64
	db           *sql.DB

	deviceID string

	mu sync.Mutex

	maintenanceCancel context.CancelFunc
	maintenanceWg     sync.WaitGroup

	testFaultInjectCommit func() error // unexported test hook
}

func NewSQLiteStorage(dbPath, identityPath string, quotaBytes int64) *SQLiteStorage {
	return &SQLiteStorage{
		dbPath:       dbPath,
		identityPath: identityPath,
		quotaBytes:   quotaBytes,
	}
}

func (s *SQLiteStorage) Init(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create db dir: %w", err)
	}

	// Check for existing corrupt databases. If found, we enter degraded state and require manual intervention.
	matches, _ := filepath.Glob(s.dbPath + ".corrupt.*")
	if len(matches) > 0 {
		return errors.New("DEGRADED_STORAGE: corrupt DB backup found, manual recovery required")
	}

	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		return err
	}

	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA temp_store = MEMORY;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			db.Close()
			return s.degradeStorage(fmt.Errorf("failed to set pragma %s: %w", p, err))
		}
	}

	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check;").Scan(&check); err != nil || check != "ok" {
		db.Close()
		return s.degradeStorage(fmt.Errorf("database corrupted or integrity check failed: %v", err))
	}

	schema := `
CREATE TABLE IF NOT EXISTS agent_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    device_id TEXT NOT NULL,
    next_seq_no INTEGER NOT NULL DEFAULT 1,
    schema_version TEXT NOT NULL,
    initialized_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    dropped_total INTEGER NOT NULL DEFAULT 0,
    dropped_debug INTEGER NOT NULL DEFAULT 0,
    dropped_info INTEGER NOT NULL DEFAULT 0,
    dropped_warning INTEGER NOT NULL DEFAULT 0,
    storage_pressure_state TEXT,
    last_pressure_at DATETIME
);

CREATE TABLE IF NOT EXISTS events (
    event_id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL,
    seq_no INTEGER NOT NULL,
    severity TEXT NOT NULL,
    payload TEXT NOT NULL,
    state TEXT NOT NULL,
    attempt_count INTEGER DEFAULT 0,
    next_attempt_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(device_id, seq_no)
);

CREATE INDEX IF NOT EXISTS idx_events_pending ON events(next_attempt_at, seq_no) WHERE state = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_events_severity ON events(severity);

CREATE TABLE IF NOT EXISTS dead_letter_queue (
    event_id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL,
    seq_no INTEGER NOT NULL,
    payload TEXT NOT NULL,
    failure_type TEXT NOT NULL,
    failure_reason TEXT NOT NULL,
    first_seen DATETIME NOT NULL,
    permanently_failed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return fmt.Errorf("failed to init schema: %w", err)
	}
	s.db = db

	if err := s.initIdentity(ctx); err != nil {
		s.db.Close()
		s.db = nil
		return err
	}

	// F01 Migration: Normalize any legacy local-offset/monotonic retry timestamps to UTC
	if err := s.migrateRetryTimestamps(ctx); err != nil {
		log.Printf("Warning: failed to migrate retry timestamps: %v", err)
	}

	// Start maintenance task
	mCtx, mCancel := context.WithCancel(context.Background())
	s.maintenanceCancel = mCancel
	s.maintenanceWg.Add(1)
	go s.maintenanceTask(mCtx)

	return nil
}

func (s *SQLiteStorage) initIdentity(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var deviceID string
	err = tx.QueryRowContext(ctx, "SELECT device_id FROM agent_state WHERE id = 1").Scan(&deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			deviceID, err = s.migrateOrGenerateID()
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO agent_state (id, device_id, next_seq_no, schema_version) VALUES (1, ?, 1, '1.0.0')", deviceID)
			if err != nil {
				return fmt.Errorf("failed to insert device_id: %w", err)
			}
		} else {
			return fmt.Errorf("failed to query agent_state: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	s.deviceID = deviceID

	if _, err := os.Stat(s.identityPath); err == nil {
		if err := os.Remove(s.identityPath); err != nil {
			log.Printf("Warning: failed to remove legacy identity file: %v", err)
		}
	}
	return nil
}

func (s *SQLiteStorage) migrateOrGenerateID() (string, error) {
	if s.identityPath != "" {
		data, err := os.ReadFile(s.identityPath)
		if err == nil {
			var id identity.Identity
			if err := json.Unmarshal(data, &id); err != nil {
				return "", fmt.Errorf("failed to parse legacy identity file: %w", err)
			}
			if id.DeviceID != "" {
				return id.DeviceID, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("failed to read legacy identity file: %w", err)
		}
	}
	return uuid.New().String(), nil
}

func (s *SQLiteStorage) Store(ctx context.Context, event *events.CanonicalEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return fmt.Errorf("%w: DB not initialized", ErrStoreFailedBeforeCommit)
	}

	if event.EventID == "" {
		return fmt.Errorf("%w: event_id must not be empty", ErrStoreFailedBeforeCommit)
	}

	// F05: Check idempotency BEFORE quota enforcement.
	// A retry of an already-committed or already-dead-lettered event must not trigger
	// destructive quota pruning of unrelated telemetry or return false quota errors.
	var existingPayload string
	var existingSeqNo int64
	err := s.db.QueryRowContext(ctx, "SELECT seq_no, payload FROM events WHERE event_id = ?", event.EventID).Scan(&existingSeqNo, &existingPayload)
	if err == nil {
		event.SeqNo = existingSeqNo
		payloadBytes, _ := event.Serialize()
		if existingPayload == string(payloadBytes) {
			return nil // Idempotent success
		}
		return fmt.Errorf("%w: integrity error: event_id %s already exists with different payload", ErrStoreFailedBeforeCommit, event.EventID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)
	}

	err = s.db.QueryRowContext(ctx, "SELECT seq_no, payload FROM dead_letter_queue WHERE event_id = ?", event.EventID).Scan(&existingSeqNo, &existingPayload)
	if err == nil {
		event.SeqNo = existingSeqNo
		payloadBytes, _ := event.Serialize()
		if existingPayload == string(payloadBytes) {
			return nil // Idempotent success
		}
		return fmt.Errorf("%w: integrity error: event_id %s already in DLQ with different payload", ErrStoreFailedBeforeCommit, event.EventID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)
	}

	// Event is verified new. Now enforce soft quota before disk allocation.
	estBytes, _ := json.Marshal(event)
	estimatedSize := int64(len(estBytes)) + 1024

	if err := s.enforceQuota(ctx, event.Severity, estimatedSize); err != nil {
		if event.Severity == "CRITICAL" || event.Severity == "FATAL" {
			log.Printf("CRITICAL TELEMETRY LOSS: storage emergency, unable to persist event: %v", err)
		}
		return fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)
	}
	defer tx.Rollback()

	// Double-check inside transaction in case of concurrent writes under different locks
	var dummy int64
	if checkErr := tx.QueryRowContext(ctx, "SELECT seq_no FROM events WHERE event_id = ?", event.EventID).Scan(&dummy); checkErr == nil {
		return nil
	}

	var nextSeqNo int64
	err = tx.QueryRowContext(ctx, "SELECT next_seq_no FROM agent_state WHERE id = 1").Scan(&nextSeqNo)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)
	}

	event.SeqNo = nextSeqNo

	payloadBytes, err := event.Serialize()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)
	}
	payloadStr := string(payloadBytes)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO events (event_id, device_id, seq_no, severity, payload, state)
		VALUES (?, ?, ?, ?, ?, 'PENDING')
	`, event.EventID, s.deviceID, event.SeqNo, event.Severity, payloadStr)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)
	}

	_, err = tx.ExecContext(ctx, "UPDATE agent_state SET next_seq_no = next_seq_no + 1 WHERE id = 1")
	if err != nil {
		return fmt.Errorf("%w: %w", ErrStoreFailedBeforeCommit, err)
	}

	if s.testFaultInjectCommit != nil {
		if err := s.testFaultInjectCommit(); err != nil {
			return fmt.Errorf("%w: test injected fault: %w", ErrStoreUncertain, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%w: %w", ErrStoreUncertain, err)
	}

	return nil
}

func (s *SQLiteStorage) enforceQuota(ctx context.Context, incomingSeverity string, estimatedSize int64) error {
	size, err := s.getTotalSize()
	if err != nil {
		return err
	}

	if size+estimatedSize < s.quotaBytes {
		return nil
	}

	prunableSeverities := []string{"DEBUG", "INFO", "WARNING"}
	for _, sev := range prunableSeverities {
		for {
			size, _ := s.getTotalSize()
			if size+estimatedSize < int64(float64(s.quotaBytes)*0.9) {
				return nil
			}
			res, err := s.db.ExecContext(ctx, `
				DELETE FROM events 
				WHERE event_id IN (
					SELECT event_id FROM events 
					WHERE severity = ? 
					ORDER BY seq_no ASC 
					LIMIT 50
				)
			`, sev)
			if err != nil {
				return err
			}
			rows, _ := res.RowsAffected()
			if rows == 0 {
				break
			}
			log.Printf("Disk pressure: pruned %d %s events", rows, sev)

			var colName string
			switch sev {
			case "WARNING":
				colName = "dropped_warning"
			case "INFO":
				colName = "dropped_info"
			case "DEBUG":
				colName = "dropped_debug"
			default:
				colName = "dropped_" + sev
			}
			_, err = s.db.ExecContext(ctx, fmt.Sprintf(`UPDATE agent_state SET dropped_total = dropped_total + %d, %s = %s + %d, storage_pressure_state = 'PRUNING', last_pressure_at = CURRENT_TIMESTAMP WHERE id = 1`, rows, colName, colName, rows))
			if err != nil {
				return fmt.Errorf("failed to update quota state: %w", err)
			}
		}
	}

	size, _ = s.getTotalSize()
	if size+estimatedSize >= s.quotaBytes {
		_, err := s.db.ExecContext(ctx, `UPDATE agent_state SET storage_pressure_state = 'EMERGENCY', last_pressure_at = CURRENT_TIMESTAMP WHERE id = 1`)
		if err != nil {
			return fmt.Errorf("failed to update emergency state: %w", err)
		}
		if incomingSeverity == "CRITICAL" || incomingSeverity == "FATAL" {
			return nil // Attempt anyway
		}
		return errors.New("STORAGE_EMERGENCY: logical soft quota exhausted and no more prunable events")
	}

	_, err = s.db.ExecContext(ctx, `UPDATE agent_state SET storage_pressure_state = 'OK' WHERE id = 1 AND storage_pressure_state != 'OK'`)
	if err != nil {
		return fmt.Errorf("failed to update OK state: %w", err)
	}
	return nil
}

func (s *SQLiteStorage) getTotalSize() (int64, error) {
	var total int64
	files := []string{s.dbPath, s.dbPath + "-wal", s.dbPath + "-shm"}
	for _, f := range files {
		if stat, err := os.Stat(f); err == nil {
			total += stat.Size()
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}
	return total, nil
}

func (s *SQLiteStorage) maintenanceTask(ctx context.Context) {
	defer s.maintenanceWg.Done()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.db != nil {
				s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE);")
			}
			s.mu.Unlock()
		}
	}
}

func (s *SQLiteStorage) Close() error {
	if s.maintenanceCancel != nil {
		s.maintenanceCancel()
		s.maintenanceWg.Wait()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteStorage) GetDeviceID() string {
	return s.deviceID
}

func (s *SQLiteStorage) GetDB() *sql.DB {
	return s.db
}

// GetPendingEvents retrieves a batch of pending events for forwarding.
// It uses a deliberately bounded batch (limit) and holds serialized storage access (s.mu)
// while decoding the bounded batch, ensuring that the storage blocking scope remains bounded.
func (s *SQLiteStorage) GetPendingEvents(ctx context.Context, limit int) ([]*events.CanonicalEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil, errors.New("DEGRADED_STORAGE: DB not initialized")
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, device_id, seq_no, payload FROM events 
		WHERE state = 'PENDING' AND (next_attempt_at IS NULL OR datetime(next_attempt_at) <= CURRENT_TIMESTAMP)
		ORDER BY seq_no ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []*events.CanonicalEvent
	type rawEv struct {
		EventID  string
		DeviceID string
		SeqNo    int64
		Payload  string
	}
	var corrupt []rawEv

	for rows.Next() {
		var r rawEv
		if err := rows.Scan(&r.EventID, &r.DeviceID, &r.SeqNo, &r.Payload); err != nil {
			return nil, err
		}
		event, err := events.Deserialize([]byte(r.Payload))
		if err != nil {
			log.Printf("Warning: failed to deserialize event %s from DB: %v. Moving to DLQ.", r.EventID, err)
			corrupt = append(corrupt, r)
			continue
		}
		results = append(results, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	for _, c := range corrupt {
		if err := s.moveRawToDLQLocked(ctx, c.EventID, c.DeviceID, c.SeqNo, c.Payload, "local_deserialization_error", "Failed to deserialize JSON"); err != nil {
			return nil, fmt.Errorf("failed to move corrupt event %s to DLQ: %w", c.EventID, err)
		}
	}

	return results, err
}

func (s *SQLiteStorage) moveRawToDLQLocked(ctx context.Context, eventID, deviceID string, seqNo int64, payload, failureType, failureReason string) error {
	// s.mu MUST already be held by caller
	if s.db == nil {
		return errors.New("DEGRADED_STORAGE: DB not initialized")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var existingPayload string
	err = tx.QueryRowContext(ctx, "SELECT payload FROM dead_letter_queue WHERE event_id = ?", eventID).Scan(&existingPayload)
	if err == nil {
		if existingPayload != payload {
			return fmt.Errorf("integrity error: event_id %s already in DLQ with different payload", eventID)
		}
		// Already in DLQ, idempotent
	} else if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO dead_letter_queue (event_id, device_id, seq_no, payload, failure_type, failure_reason, first_seen)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, eventID, deviceID, seqNo, payload, failureType, failureReason)
		if err != nil {
			return err
		}
	} else {
		return err
	}

	_, err = tx.ExecContext(ctx, "DELETE FROM events WHERE event_id = ?", eventID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStorage) RemoveEvent(ctx context.Context, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("DEGRADED_STORAGE: DB not initialized")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE event_id = ?", eventID)
	return err
}

const (
	maxBackoffSeconds    = 3600  // 1 hour cap
	maxRetryAfterSeconds = 86400 // 24 hours max server-specified retry-after
)

func (s *SQLiteStorage) MarkFailed(ctx context.Context, eventID string, retryAfter int, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return errors.New("DEGRADED_STORAGE: DB not initialized")
	}

	var attemptCount int
	if err := s.db.QueryRowContext(ctx, "SELECT attempt_count FROM events WHERE event_id = ?", eventID).Scan(&attemptCount); err != nil {
		return err
	}

	attemptCount++
	if attemptCount < 0 {
		attemptCount = 1
	}

	var nextAttemptAt time.Time
	if retryAfter > 0 {
		// F13: Bounded retry-after against integer/duration overflow
		if retryAfter > maxRetryAfterSeconds {
			retryAfter = maxRetryAfterSeconds
		}
		nextAttemptAt = time.Now().UTC().Add(time.Duration(retryAfter) * time.Second)
	} else {
		// F13: Saturate exponential backoff arithmetic before shifting
		// Formula: next_attempt_at = now + min(MaxBackoff, InitialBackoff * 2^attempt_count) + Jitter
		// For attemptCount >= 10, 5 * (1 << 10) = 5120 > 3600 (maxBackoffSeconds)
		var backoff int64
		if attemptCount >= 10 {
			backoff = maxBackoffSeconds
		} else {
			backoff = int64(5 * (1 << attemptCount))
			if backoff > maxBackoffSeconds {
				backoff = maxBackoffSeconds
			}
		}
		jitter := time.Duration(time.Now().UnixNano()%10) * time.Second // Bounded jitter up to 10s
		nextAttemptAt = time.Now().UTC().Add(time.Duration(backoff)*time.Second + jitter)
	}

	// F01: Format as canonical UTC text matching SQLite CURRENT_TIMESTAMP format ("YYYY-MM-DD HH:MM:SS")
	formattedTS := nextAttemptAt.UTC().Format("2006-01-02 15:04:05")

	_, updateErr := s.db.ExecContext(ctx, `
		UPDATE events SET attempt_count = ?, next_attempt_at = ? WHERE event_id = ?
	`, attemptCount, formattedTS, eventID)
	return updateErr
}

// migrateRetryTimestamps normalizes legacy or local-offset retry timestamps in existing rows to UTC.
func (s *SQLiteStorage) migrateRetryTimestamps(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT event_id, next_attempt_at FROM events WHERE next_attempt_at IS NOT NULL")
	if err != nil {
		return fmt.Errorf("failed to query retry timestamps: %w", err)
	}
	defer rows.Close()

	type toUpdate struct {
		eventID   string
		formatted string
	}
	var updates []toUpdate

	for rows.Next() {
		var eventID string
		var rawTS string
		if err := rows.Scan(&eventID, &rawTS); err != nil {
			return err
		}
		// If rawTS already matches canonical SQLite UTC "YYYY-MM-DD HH:MM:SS" (len 19), no migration needed.
		if len(rawTS) == 19 && rawTS[4] == '-' && rawTS[7] == '-' && rawTS[10] == ' ' && rawTS[13] == ':' && rawTS[16] == ':' {
			continue
		}
		parsed, ok := parseRetryTimestamp(rawTS)
		var newTS string
		if ok {
			newTS = parsed.UTC().Format("2006-01-02 15:04:05")
		} else {
			// Unparseable legacy string: set to past UTC so it becomes immediately eligible rather than stuck
			newTS = time.Now().UTC().Add(-1 * time.Hour).Format("2006-01-02 15:04:05")
		}
		updates = append(updates, toUpdate{eventID: eventID, formatted: newTS})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, u := range updates {
		_, err := s.db.ExecContext(ctx, "UPDATE events SET next_attempt_at = ? WHERE event_id = ?", u.formatted, u.eventID)
		if err != nil {
			log.Printf("Warning: failed to migrate retry timestamp for event %s: %v", u.eventID, err)
		}
	}
	return nil
}

// parseRetryTimestamp parses legacy and variant timestamp strings into UTC time.
func parseRetryTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	// Strip Go monotonic suffix " m=+..." if present
	if idx := strings.Index(raw, " m="); idx != -1 {
		raw = strings.TrimSpace(raw[:idx])
	}

	// 1. Standard SQLite UTC format
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", raw, time.UTC); err == nil {
		return t.UTC(), true
	}
	// 2. RFC3339 formats
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), true
	}
	// 3. Go String() formats (with timezone name/offset)
	layouts := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 -0700",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05 -0700",
		"2006-01-02 15:04:05.999999999 +0700 MST",
		"2006-01-02 15:04:05.999999999 +0700",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func (s *SQLiteStorage) MoveToDLQ(ctx context.Context, event *events.CanonicalEvent, failureType, failureReason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload, err := event.Serialize()
	if err != nil {
		return err
	}
	return s.moveRawToDLQLocked(ctx, event.EventID, s.deviceID, event.SeqNo, string(payload), failureType, failureReason)
}

func (s *SQLiteStorage) degradeStorage(err error) error {
	corruptPath := fmt.Sprintf("%s.corrupt.%d", s.dbPath, time.Now().Unix())
	if rErr := os.Rename(s.dbPath, corruptPath); rErr != nil && !errors.Is(rErr, os.ErrNotExist) {
		return fmt.Errorf("DEGRADED_STORAGE: %w (additionally failed to move DB: %v)", err, rErr)
	}
	if rErr := os.Rename(s.dbPath+"-wal", corruptPath+"-wal"); rErr != nil && !errors.Is(rErr, os.ErrNotExist) {
		return fmt.Errorf("DEGRADED_STORAGE: %w (additionally failed to move WAL: %v)", err, rErr)
	}
	if rErr := os.Rename(s.dbPath+"-shm", corruptPath+"-shm"); rErr != nil && !errors.Is(rErr, os.ErrNotExist) {
		return fmt.Errorf("DEGRADED_STORAGE: %w (additionally failed to move SHM: %v)", err, rErr)
	}
	return fmt.Errorf("DEGRADED_STORAGE: %w (moved to %s)", err, corruptPath)
}
