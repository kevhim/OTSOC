package queue

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"redcyberfox/pkg/events"
)

type Producer struct {
	client *redis.Client
	stream string
}

func NewProducer(client *redis.Client, stream string) *Producer {
	return &Producer{
		client: client,
		stream: stream,
	}
}

// Publish normalizes the CanonicalEvent and queues it into Valkey
func (p *Producer) Publish(ctx context.Context, ev *events.CanonicalEvent) error {
	data, err := ev.Serialize()
	if err != nil {
		return fmt.Errorf("failed to serialize event: %w", err)
	}

	// We use the event_id as the message ID if possible, but XADD auto ID (*) is safer
	// for avoiding clock skew issues in stream IDs. The actual event_id is in the payload.
	err = p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		Values: map[string]interface{}{
			"payload": data,
		},
	}).Err()

	if err != nil {
		return fmt.Errorf("failed to publish to valkey stream %s: %w", p.stream, err)
	}
	return nil
}
