package events

import (
	"context"
	"encoding/json"
	"errors"
	"miniplay/internal/game"
	"miniplay/internal/store"
	"testing"
	"time"
)

type fakeStats struct {
	seen    map[string]bool
	applied int
}

func (s *fakeStats) ApplyEvent(_ context.Context, event store.Event) (bool, error) {
	if s.seen[event.ID] {
		return false, nil
	}
	s.seen[event.ID] = true
	s.applied++
	return true, nil
}

type topicProducer struct {
	topic string
	count int
}

type repairBoards struct {
	fail    bool
	updates []store.Score
}

func (b *repairBoards) UpdateBoards(_ context.Context, row store.Score) error {
	if b.fail {
		return errors.New("Redis unavailable")
	}
	b.updates = append(b.updates, row)
	return nil
}

func (p *topicProducer) Publish(_ context.Context, topic, _ string, _ []byte) error {
	p.topic = topic
	p.count++
	return nil
}

func TestWorkerDeduplicatesReplayAndDeadLettersBadInput(t *testing.T) {
	stats := &fakeStats{seen: map[string]bool{}}
	boards := &repairBoards{}
	producer := &topicProducer{}
	event := store.Event{ID: "accepted:run", Kind: "score.accepted", RunID: "run", User: "alice", Game: game.Rhythm, Score: 10, At: time.Now()}
	payload, _ := json.Marshal(event)
	for i := 0; i < 2; i++ {
		if _, err := Process(context.Background(), payload, stats, boards, producer); err != nil {
			t.Fatal(err)
		}
	}
	if stats.applied != 1 || len(boards.updates) != 2 {
		t.Fatalf("applied=%d board repairs=%d", stats.applied, len(boards.updates))
	}
	if _, err := Process(context.Background(), []byte("not json"), stats, boards, producer); err != nil {
		t.Fatal(err)
	}
	if producer.topic != DeadLetterTopic || producer.count != 1 {
		t.Fatalf("deadletter=%+v", producer)
	}
}

func TestWorkerRepairsBoardAfterSQLCommitAndRedisFailure(t *testing.T) {
	stats := &fakeStats{seen: map[string]bool{}}
	boards := &repairBoards{fail: true}
	producer := &topicProducer{}
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	event := store.Event{ID: "accepted:run", Kind: "score.accepted", RunID: "run", User: "alice", Game: game.Rhythm, Score: 20, At: at}
	payload, _ := json.Marshal(event)
	if applied, err := Process(context.Background(), payload, stats, boards, producer); !applied || err == nil {
		t.Fatalf("first attempt applied=%t err=%v", applied, err)
	}
	if stats.applied != 1 {
		t.Fatalf("SQL applied %d times", stats.applied)
	}
	// The caller must leave the Kafka offset uncommitted on that error. A
	// restarted worker sees the same record, skips the SQL aggregate, and
	// still repairs the cache before the offset can be committed.
	boards.fail = false
	if applied, err := Process(context.Background(), payload, stats, boards, producer); applied || err != nil {
		t.Fatalf("replayed attempt applied=%t err=%v", applied, err)
	}
	if stats.applied != 1 || len(boards.updates) != 1 || boards.updates[0].SubmittedAt != at || boards.updates[0].Score != 20 {
		t.Fatalf("SQL applications=%d repairs=%+v", stats.applied, boards.updates)
	}
}

func TestWorkerDoesNotRepairRejectedBoard(t *testing.T) {
	stats := &fakeStats{seen: map[string]bool{}}
	boards := &repairBoards{}
	producer := &topicProducer{}
	event := store.Event{ID: "rejected:run", Kind: "score.rejected", RunID: "run", User: "alice", Game: game.Rhythm, Reason: "score mismatch", At: time.Now()}
	payload, _ := json.Marshal(event)
	if applied, err := Process(context.Background(), payload, stats, boards, producer); !applied || err != nil {
		t.Fatalf("applied=%t err=%v", applied, err)
	}
	if len(boards.updates) != 0 {
		t.Fatalf("unexpected board update: %+v", boards.updates)
	}
}
