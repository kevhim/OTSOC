package queue

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	
	"redcyberfox/server/internal/db"
	"redcyberfox/server/pkg/events"
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
				c.processMessage(ctx, msg)
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
		Count:  100,
	}).Result()

	if err != nil {
		return err
	}

	for _, p := range pending {
		// Reclaim if idle for more than 1 minute
		if p.Idle > time.Minute {
			log.Printf("Reclaiming idle message %s", p.ID)
			c.client.XClaim(ctx, &redis.XClaimArgs{
				Stream:   c.stream,
				Group:    c.group,
				Consumer: c.consumer,
				MinIdle:  time.Minute,
				Messages: []string{p.ID},
			})
			
			// If it's claimed a ridiculous amount of times, it's a poison pill
			if p.RetryCount > 5 {
				log.Printf("Poison pill detected: %s. ACKing to drop.", p.ID)
				c.client.XAck(ctx, c.stream, c.group, p.ID)
				continue
			}

			// We fetch the message explicitly to process it
			msgs, err := c.client.XRange(ctx, c.stream, p.ID, p.ID).Result()
			if err == nil && len(msgs) > 0 {
				c.processMessage(ctx, msgs[0])
			}
		}
	}
	return nil
}

func (c *Consumer) processMessage(ctx context.Context, msg redis.XMessage) {
	dataRaw, ok := msg.Values["payload"].(string)
	if !ok {
		log.Printf("Invalid payload type for message %s, ACKing as poison", msg.ID)
		c.client.XAck(ctx, c.stream, c.group, msg.ID)
		return
	}

	ev, err := events.Deserialize([]byte(dataRaw))
	if err != nil {
		log.Printf("Failed to deserialize event %s, ACKing as poison: %v", msg.ID, err)
		c.client.XAck(ctx, c.stream, c.group, msg.ID)
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
