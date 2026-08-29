package queue

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	
	"redcyberfox/server/internal/db"
	"redcyberfox/pkg/events"
)

const (
	RecoveryInterval  = 1 * time.Minute
	MinIdleTime       = 1 * time.Minute
	RecoveryBatchSize = 100
)

type Consumer struct {
	client    *redis.Client
	repo      *db.Repository
	stream    string
	group     string
	consumer  string
}

func NewConsumer(client *redis.Client, repo *db.Repository, stream, group, consumer string) *Consumer {
	return &Consumer{
		client:   client,
		repo:     repo,
		stream:   stream,
		group:    group,
		consumer: consumer,
	}
}

// Start begins processing the Valkey stream using At-Least-Once delivery.
// ACK ONLY happens AFTER Postgres commit succeeds.
func (c *Consumer) Start(ctx context.Context) error {
	// Initialize group (ignore if exists)
	err := c.client.XGroupCreateMkStream(ctx, c.stream, c.group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}

	// 1. Recover pending messages (unACKed)
	if err := c.recoverPending(ctx); err != nil {
		log.Printf("Error recovering pending messages: %v", err)
	}

	// 2. Main processing loop
	log.Printf("Starting worker processing on stream %s", c.stream)
	for {
		select {
		case <-ctx.Done():
			log.Println("Context done, stopping consumer")
			return nil
		default:
		}

		// Read new messages from group
		streams, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.group,
			Consumer: c.consumer,
			Streams:  []string{c.stream, ">"},
			Count:    10,
			Block:    2 * time.Second,
		}).Result()

		if err != nil {
			if errors.Is(err, redis.Nil) { // No new messages
				continue
			}
			// Transient network failure
			log.Printf("XReadGroup error: %v", err)
			time.Sleep(1 * time.Second)
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				c.processMessage(ctx, msg, 0, 0)
			}
		}
	}
}

// recoverPending claims and processes unACKed messages from crashed workers
func (c *Consumer) recoverPending(ctx context.Context) error {
	log.Println("Checking for pending (unACKed) messages...")

	pending, err := c.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: c.stream,
		Group:  c.group,
		Start:  "-",
		End:    "+",
		Count:  RecoveryBatchSize, // Bounded batch to prevent blocking forever if backlog is huge
	}).Result()

	if err != nil {
		return err
	}

	for _, p := range pending {
		// Reclaim if idle for more than configured MinIdleTime
		if p.Idle > MinIdleTime {
			log.Printf("Reclaiming idle message %s", p.ID)
			c.client.XClaim(ctx, &redis.XClaimArgs{
				Stream:   c.stream,
				Group:    c.group,
				Consumer: c.consumer,
				MinIdle:  MinIdleTime,
				Messages: []string{p.ID},
			})
			// We fetch the message explicitly to process it
			msgs, err := c.client.XRange(ctx, c.stream, p.ID, p.ID).Result()
			if err == nil && len(msgs) > 0 {
				msg := msgs[0]
				if p.RetryCount > 5 {
					log.Printf("Retry exhausted for %s. Sending to DLQ.", p.ID)
					payload := ""
					if pRaw, ok := msg.Values["payload"].(string); ok {
						payload = pRaw
					}
					if err := c.sendToDLQ(ctx, p.ID, payload, "retry limit exceeded", "retry_exhausted", p.RetryCount, p.Idle); err == nil {
						c.client.XAck(ctx, c.stream, c.group, p.ID)
					} else {
						log.Printf("Failed to push %s to DLQ: %v", p.ID, err)
					}
				} else {
					c.processMessage(ctx, msg, p.RetryCount, p.Idle)
				}
			}
		}
	}
	return nil
}

func (c *Consumer) sendToDLQ(ctx context.Context, msgID string, payload string, reason string, fType string, retryCount int64, idle time.Duration) error {
	now := time.Now().UTC()
	firstFailure := now.Add(-idle)

	err := c.client.XAdd(ctx, &redis.XAddArgs{
		Stream: "ot_events_dlq",
		Values: map[string]interface{}{
			"original_stream":         c.stream,
			"original_message_id":     msgID,
			"original_payload":        payload,
			"failure_reason":          reason,
			"failure_type":            fType,
			"first_failure_timestamp": firstFailure.Format(time.RFC3339),
			"last_failure_timestamp":  now.Format(time.RFC3339),
			"retry_count":             retryCount,
		},
	}).Err()
	return err
}

func (c *Consumer) processMessage(ctx context.Context, msg redis.XMessage, retryCount int64, idle time.Duration) {
	dataRaw, ok := msg.Values["payload"].(string)
	if !ok {
		log.Printf("Invalid payload type for message %s, ACKing as poison", msg.ID)
		if err := c.sendToDLQ(ctx, msg.ID, fmt.Sprintf("%v", msg.Values["payload"]), "payload not a string", "unmarshal", retryCount, idle); err == nil {
			c.client.XAck(ctx, c.stream, c.group, msg.ID)
		}
		return
	}

	ev, err := events.Deserialize([]byte(dataRaw))
	if err != nil {
		log.Printf("Failed to deserialize event %s, ACKing as poison: %v", msg.ID, err)
		if err := c.sendToDLQ(ctx, msg.ID, dataRaw, err.Error(), "unmarshal", retryCount, idle); err == nil {
			c.client.XAck(ctx, c.stream, c.group, msg.ID)
		}
		return
	}
	
	if err := ev.Validate(); err != nil {
		log.Printf("Failed to validate event %s, ACKing as poison: %v", msg.ID, err)
		if err := c.sendToDLQ(ctx, msg.ID, dataRaw, err.Error(), "validation", retryCount, idle); err == nil {
			c.client.XAck(ctx, c.stream, c.group, msg.ID)
		}
		return
	}

	// At-least-once delivery + Idempotent Persistence
	_, err = c.repo.PersistEvent(ctx, ev)
	if err != nil {
		// Postgres persistence failed! We do NOT ACK.
		// It will remain pending and be picked up on retry.
		log.Printf("Persistence failed for %s (msg: %s): %v. Will not ACK.", ev.EventID, msg.ID, err)
		return
	}

	// 3. ACK ONLY after successful persistence
	if err := c.client.XAck(ctx, c.stream, c.group, msg.ID).Err(); err != nil {
		log.Printf("Failed to ACK message %s: %v", msg.ID, err)
	} else {
		log.Printf("Successfully processed and ACKed msg %s (event %s)", msg.ID, ev.EventID)
	}
}
