package filesystem

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"redcyberfox/agent/internal/config"
	"redcyberfox/pkg/events"

	"github.com/fsnotify/fsnotify"
	"github.com/google/uuid"
)

type Collector struct {
	cfg     *config.Config
	watcher *fsnotify.Watcher
	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewCollector(cfg *config.Config) *Collector {
	return &Collector{
		cfg: cfg,
	}
}

func (c *Collector) Start(ctx context.Context, out chan<- *events.CanonicalEvent) error {
	if len(c.cfg.MonitorPaths) == 0 {
		log.Println("Filesystem collector: no monitor_paths configured, skipping")
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}
	c.watcher = watcher

	for _, p := range c.cfg.MonitorPaths {
		absPath, err := filepath.Abs(p)
		if err != nil {
			log.Printf("Filesystem collector: failed to resolve absolute path %s: %v", p, err)
			continue
		}
		err = c.watcher.Add(absPath)
		if err != nil {
			log.Printf("Filesystem collector: failed to watch path %s: %v", absPath, err)
			// Continue to next path, gracefully degrade
		} else {
			log.Printf("Filesystem collector: watching path %s", p)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.wg.Add(1)
	go c.run(runCtx, out)

	return nil
}

func (c *Collector) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	
	if c.watcher != nil {
		err := c.watcher.Close()
		if err != nil {
			return err
		}
	}

	c.wg.Wait()
	return nil
}

func (c *Collector) run(ctx context.Context, out chan<- *events.CanonicalEvent) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-c.watcher.Events:
			if !ok {
				return
			}
			c.handleEvent(ctx, event, out)
		case err, ok := <-c.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("Filesystem collector error: %v", err)
		}
	}
}

func (c *Collector) handleEvent(ctx context.Context, event fsnotify.Event, out chan<- *events.CanonicalEvent) {
	action := ""
	if event.Has(fsnotify.Create) {
		action = "FILE_CREATE"
	} else if event.Has(fsnotify.Write) {
		action = "FILE_MODIFY"
	} else if event.Has(fsnotify.Remove) {
		action = "FILE_DELETE"
	} else if event.Has(fsnotify.Rename) {
		action = "FILE_RENAME"
	}

	if action == "" {
		return
	}

	// Gather file metadata if possible
	var size int64
	var isDir bool
	var statOk bool
	
	if action != "FILE_DELETE" && action != "FILE_RENAME" {
		info, err := os.Stat(event.Name)
		if err == nil {
			size = info.Size()
			isDir = info.IsDir()
			statOk = true
		}
	}

	absEventPath, err := filepath.Abs(event.Name)
	if err != nil {
		absEventPath = event.Name
	}

	ev := &events.CanonicalEvent{
		EventID:       uuid.New().String(),
		TenantID:      c.cfg.TenantID,
		SiteID:        c.cfg.SiteID,
		OccurredAt:    time.Now().UTC(),
		Source:        "agent",
		Category:      "filesystem",
		Action:        action,
		Severity:      "INFO",
		SchemaVersion: events.CurrentSchemaVersion,
		Metadata: map[string]interface{}{
			"file_path": absEventPath,
		},
	}
	
	if statOk {
		ev.Metadata["size"] = size
	}
	if statOk && isDir {
		ev.Metadata["is_dir"] = true
	}

	timer := time.NewTimer(1 * time.Second)
	defer timer.Stop()

	select {
	case out <- ev:
		return
	case <-ctx.Done():
		log.Printf("[ERROR] Filesystem collector shutting down; discarding observed event: %s %s", ev.Action, ev.Metadata["file_path"])
		return
	case <-timer.C:
		// Downstream is stalled. Emit an observable health signal.
		log.Printf("[ERROR] Filesystem collector output channel blocked for >1s, downstream pipeline is stalled. Emitting health failure and continuing to block to prevent event loss.")
		// We must not discard the event. Continue blocking indefinitely.
		select {
		case out <- ev:
			log.Printf("[INFO] Filesystem collector downstream pipeline recovered.")
			return
		case <-ctx.Done():
			log.Printf("[ERROR] Filesystem collector shutting down while blocked on stalled pipeline; discarding observed event: %s %s", ev.Action, ev.Metadata["file_path"])
			return
		}
	}
}
