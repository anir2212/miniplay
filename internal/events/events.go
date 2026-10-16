package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

const Topic = "score.events"
const DeadLetterTopic = "score.deadletter"

func Validate(event store.Event) error {
	if !game.ValidID(event.Game) || event.RunID == "" || event.User == "" || event.At.IsZero() || event.At.After(time.Now().Add(5*time.Minute)) {
		return errors.New("missing or invalid event identity")
	}
	if event.Kind == "score.accepted" {
		if event.ID != "accepted:"+event.RunID || event.Score < 0 || event.Score > 390 || event.Reason != "" {
			return errors.New("invalid accepted event")
		}
	} else if event.Kind == "score.rejected" {
		if event.ID != "rejected:"+event.RunID || event.Reason == "" {
			return errors.New("invalid rejected event")
		}
	} else {
		return errors.New("unknown event kind")
	}
	return nil
}

type Outbox interface {
	Pending(context.Context, int) ([]store.OutboxRow, error)
	MarkPublished(context.Context, string) error
}
type Publisher interface {
	Publish(context.Context, string, string, []byte) error
}

// PublishOnce is deliberately at-least-once: a crash after Kafka ACK and
// before MarkPublished sends the same ID again. The worker deduplicates in SQL.
func PublishOnce(ctx context.Context, outbox Outbox, producer Publisher, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("invalid batch size")
	}
	rows, err := outbox.Pending(ctx, limit)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, row := range rows {
		if strings.TrimSpace(row.ID) == "" || len(row.Payload) == 0 {
			return completed, errors.New("invalid outbox row")
		}
		if err := producer.Publish(ctx, Topic, row.ID, row.Payload); err != nil {
			return completed, err
		}
		if err := outbox.MarkPublished(ctx, row.ID); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}
