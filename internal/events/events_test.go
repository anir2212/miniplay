package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

type fakeOutbox struct {
	rows     []store.OutboxRow
	marked   bool
	failMark bool
}

func (o *fakeOutbox) Pending(context.Context, int) ([]store.OutboxRow, error) {
	if o.marked {
		return nil, nil
	}
	return o.rows, nil
}
func (o *fakeOutbox) MarkPublished(context.Context, string) error {
	if o.failMark {
		return errors.New("database interrupted")
	}
	o.marked = true
	return nil
}

type fakeProducer struct{ ids []string }

func (p *fakeProducer) Publish(_ context.Context, topic, id string, _ []byte) error {
	if topic != Topic {
		return errors.New("bad topic")
	}
	p.ids = append(p.ids, id)
	return nil
}

func TestOutboxResendsAfterMarkFailure(t *testing.T) {
	o := &fakeOutbox{rows: []store.OutboxRow{{ID: "accepted:run", Payload: []byte(`{"kind":"score.accepted"}`)}}, failMark: true}
	p := &fakeProducer{}
	if _, err := PublishOnce(context.Background(), o, p, 100); err == nil {
		t.Fatal("expected failed mark")
	}
	o.failMark = false
	n, err := PublishOnce(context.Background(), o, p, 100)
	if err != nil || n != 1 || len(p.ids) != 2 || p.ids[0] != p.ids[1] {
		t.Fatalf("n=%d ids=%v err=%v", n, p.ids, err)
	}
}

func TestValidateEvent(t *testing.T) {
	event := store.Event{ID: "accepted:run", Kind: "score.accepted", RunID: "run", User: "alice", Game: game.Rhythm, Score: 10, At: time.Now()}
	if err := Validate(event); err != nil {
		t.Fatal(err)
	}
	event.Score = 999
	if err := Validate(event); err == nil {
		t.Fatal("impossible score accepted")
	}
}
