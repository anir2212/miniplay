package events

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	"miniplay/internal/store"
)

type StatsStore interface {
	ApplyEvent(context.Context, store.Event) (bool, error)
}
type BoardCache interface {
	UpdateBoards(context.Context, store.Score) error
}
type DeadLetter struct {
	Reason    string `json:"reason"`
	RawBase64 string `json:"rawBase64"`
}

// Process delivers malformed events to a dead-letter topic before the caller
// commits its Kafka offset. Valid events get a SQL idempotency transaction.
// Accepted events also repair Redis before offset commit. This repair runs even
// when SQL has already seen the event: a crash or Redis failure after the SQL
// commit must not leave an existing leaderboard key permanently stale.
func Process(ctx context.Context, payload []byte, stats StatsStore, boards BoardCache, producer Publisher) (bool, error) {
	event, err := store.DecodeEvent(payload)
	if err == nil {
		err = Validate(event)
	}
	if err != nil {
		body, _ := json.Marshal(DeadLetter{Reason: err.Error(), RawBase64: base64.StdEncoding.EncodeToString(payload)})
		digest := sha256.Sum256(payload)
		if publishErr := producer.Publish(ctx, DeadLetterTopic, hex.EncodeToString(digest[:]), body); publishErr != nil {
			return false, publishErr
		}
		return false, nil
	}
	applied, err := stats.ApplyEvent(ctx, event)
	if err != nil {
		return false, err
	}
	if event.Kind == "score.accepted" {
		row := store.Score{RunID: event.RunID, User: event.User, Game: event.Game, Score: event.Score, SubmittedAt: event.At}
		if err := boards.UpdateBoards(ctx, row); err != nil {
			return applied, err
		}
	}
	return applied, nil
}
