package forwarder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"redcyberfox/agent/internal/interfaces"
	"redcyberfox/pkg/events"
)

type Forwarder struct {
	apiURL     string
	tenantID   string
	httpClient *http.Client
	storage    interfaces.Storage

	mu      sync.Mutex
	wakeup  chan struct{}
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	stopped bool
}

func NewForwarder(apiURL, tenantID string, storage interfaces.Storage) *Forwarder {
	return &Forwarder{
		apiURL:   apiURL,
		tenantID: tenantID,
		storage:  storage,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		wakeup: make(chan struct{}, 1),
	}
}

// Wakeup can be called when new events are stored
func (f *Forwarder) Wakeup() {
	select {
	case f.wakeup <- struct{}{}:
	default:
	}
}

// Start begins the forwarder loop in a background goroutine.
// Lifecycle contract:
// - A Forwarder instance may be started exactly once.
// - Start() after a successful start returns an error.
// - The forwarder is not restartable; once stopped, it cannot be started again.
func (f *Forwarder) Start(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.stopped {
		return fmt.Errorf("forwarder cannot be restarted")
	}
	if f.started {
		return fmt.Errorf("forwarder already started")
	}

	f.started = true
	runCtx, cancel := context.WithCancel(ctx)
	f.cancel = cancel

	f.wg.Add(1)
	go f.run(runCtx)
	return nil
}

// Stop gracefully shuts down the forwarder.
// Lifecycle contract:
// - Stop() is idempotent and can be called multiple times safely.
// - Stop() cancels the internal context and waits for the run goroutine to exit.
func (f *Forwarder) Stop() error {
	f.mu.Lock()
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	f.stopped = true
	f.mu.Unlock()

	f.wg.Wait()

	return nil
}

func (f *Forwarder) run(ctx context.Context) {
	defer f.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		f.processPending(ctx)

		select {
		case <-ctx.Done():
			return
		case <-f.wakeup:
		case <-ticker.C:
		}
	}
}

func (f *Forwarder) processPending(ctx context.Context) {
	eventsList, err := f.storage.GetPendingEvents(ctx, 50)
	if err != nil {
		if err.Error() != "DEGRADED_STORAGE: DB not initialized" {
			log.Printf("Forwarder failed to get pending events: %v", err)
		}
		return
	}

	for _, ev := range eventsList {
		if ctx.Err() != nil {
			return
		}
		f.sendEvent(ctx, ev)
	}
}

func (f *Forwarder) sendEvent(ctx context.Context, event *events.CanonicalEvent) {
	if event.TenantID != f.tenantID {
		err := f.storage.MoveToDLQ(ctx, event, "tenant_mismatch", fmt.Sprintf("Event tenant %s != forwarder tenant %s", event.TenantID, f.tenantID))
		if err != nil {
			log.Printf("CRITICAL: Failed to move tenant mismatched event %s to DLQ: %v", event.EventID, err)
		}
		return
	}

	payload, err := json.Marshal(event)
	if err != nil {
		err = f.storage.MoveToDLQ(ctx, event, "serialization_error", err.Error())
		if err != nil {
			log.Printf("CRITICAL: Failed to move serialization-error event %s to DLQ: %v", event.EventID, err)
		}
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", f.apiURL+"/v1/ingest", bytes.NewReader(payload))
	if err != nil {
		if err := f.storage.MarkFailed(ctx, event.EventID, 0, err); err != nil {
			log.Printf("CRITICAL: Failed to mark event %s as failed (req creation): %v", event.EventID, err)
		}
		return
	}

	req.Header.Set("Content-Type", "application/json")
	q := req.URL.Query()
	q.Add("tenant_id", f.tenantID)
	req.URL.RawQuery = q.Encode()

	resp, err := f.httpClient.Do(req)
	if err != nil {
		if err := f.storage.MarkFailed(ctx, event.EventID, 0, err); err != nil {
			log.Printf("CRITICAL: Failed to mark event %s as failed (http do): %v", event.EventID, err)
		}
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusAccepted { // 202
		if err := f.storage.RemoveEvent(ctx, event.EventID); err != nil {
			log.Printf("CRITICAL: Failed to remove event %s after 202 response: %v", event.EventID, err)
		}
		return
	}

	if resp.StatusCode == http.StatusBadRequest { // 400
		bodyStr := string(body)
		if len(bodyStr) > 512 {
			bodyStr = bodyStr[:512] + "...(truncated)"
		}
		if err := f.storage.MoveToDLQ(ctx, event, "http_400", bodyStr); err != nil {
			log.Printf("CRITICAL: Failed to move event %s to DLQ after 400: %v", event.EventID, err)
		}
		return
	}

	// For 429, respect Retry-After
	retryAfter := 0
	if resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if s, err := strconv.Atoi(ra); err == nil {
				retryAfter = s
			}
		}
	}

	bodyStr := string(body)
	if len(bodyStr) > 512 {
		bodyStr = bodyStr[:512] + "...(truncated)"
	}

	if err := f.storage.MarkFailed(ctx, event.EventID, retryAfter, fmt.Errorf("HTTP %d: %s", resp.StatusCode, bodyStr)); err != nil {
		log.Printf("CRITICAL: Failed to mark event %s as failed (HTTP %d): %v", event.EventID, resp.StatusCode, err)
	}
}
