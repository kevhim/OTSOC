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
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"redcyberfox/agent/internal/identity"
	"redcyberfox/pkg/events"
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
		return errors.New("DEGRADED_STORAGE: DB not initialized")
	}

	isNewEventID := false
	if event.EventID == "" {
		event.EventID = uuid.New().String()
		isNewEventID = true
	}

	// Estimate payload size for quota enforcement
	estBytes, _ := json.Marshal(event)
	estimatedSize := int64(len(estBytes)) + 1024

	if err := s.enforceQuota(ctx, event.Severity, estimatedSize); err != nil {
		if event.Severity == "CRITICAL" || event.Severity == "FATAL" {
			log.Printf("CRITICAL TELEMETRY LOSS: storage emergency, unable to persist event: %v", err)
		}
		return err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if !isNewEventID {
		var existingPayload string
		var existingSeqNo int64
		err := tx.QueryRowContext(ctx, "SELECT seq_no, payload FROM events WHERE event_id = ?", event.EventID).Scan(&existingSeqNo, &existingPayload)
		if err == nil {
			event.SeqNo = existingSeqNo
			payloadBytes, _ := event.Serialize()
			if existingPayload == string(payloadBytes) {
				return nil // Idempotent success
			}
			return fmt.Errorf("integrity error: event_id %s already exists with different payload", event.EventID)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		err = tx.QueryRowContext(ctx, "SELECT seq_no, payload FROM dead_letter_queue WHERE event_id = ?", event.EventID).Scan(&existingSeqNo, &existingPayload)
		if err == nil {
			event.SeqNo = existingSeqNo
			payloadBytes, _ := event.Serialize()
			if existingPayload == string(payloadBytes) {
				return nil // Idempotent success
			}
			return fmt.Errorf("integrity error: event_id %s already in DLQ with different payload", event.EventID)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	var nextSeqNo int64
	err = tx.QueryRowContext(ctx, "SELECT next_seq_no FROM agent_state WHERE id = 1").Scan(&nextSeqNo)
	if err != nil {
		return err
	}

	event.SeqNo = nextSeqNo

	payloadBytes, err := event.Serialize()
	if err != nil {
		return err
	}
	payloadStr := string(payloadBytes)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO events (event_id, device_id, seq_no, severity, payload, state)
		VALUES (?, ?, ?, ?, ?, 'PENDING')
	`, event.EventID, s.deviceID, event.SeqNo, event.Severity, payloadStr)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "UPDATE agent_state SET next_seq_no = next_seq_no + 1 WHERE id = 1")
	if err != nil {
		return err
	}

	return tx.Commit()
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

			colName := "dropped_" + sev
			if sev == "WARNING" {
				colName = "dropped_warning"
			} else if sev == "INFO" {
				colName = "dropped_info"
			} else if sev == "DEBUG" {
				colName = "dropped_debug"
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
		WHERE state = 'PENDING' AND (next_attempt_at IS NULL OR next_attempt_at <= CURRENT_TIMESTAMP)
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

	var nextAttemptAt time.Time
	if retryAfter > 0 {
		nextAttemptAt = time.Now().Add(time.Duration(retryAfter) * time.Second)
	} else {
		// Exponential backoff + jitter
		// Formula: next_attempt_at = now + min(MaxBackoff, InitialBackoff * 2^attempt_count) + Jitter
		backoff := 5 * (1 << attemptCount)
		if backoff > 3600 { // Max 1 hour
			backoff = 3600
		}
		jitter := time.Duration(time.Now().UnixNano()%10) * time.Second // Simple jitter up to 10s
		nextAttemptAt = time.Now().Add(time.Duration(backoff)*time.Second + jitter)
	}

	_, updateErr := s.db.ExecContext(ctx, `
		UPDATE events SET attempt_count = ?, next_attempt_at = ? WHERE event_id = ?
	`, attemptCount, nextAttemptAt, eventID)
	return updateErr
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
