package httpapi

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

// broadcaster shares one Redis subscription and one board read per change
// among all live sockets on the same board key in this process. Before it,
// every socket held its own subscription and rebuilt the frame itself (a
// completeness check, a top-20 read and a rank lookup), so one score cost
// three Redis round trips per open socket.
type broadcaster struct {
	db    BoardStore
	cache LiveCache

	mu   sync.Mutex
	hubs map[string]*boardHub
}

// boardHub serves one board key. It exists while at least one socket is
// subscribed to that key.
type boardHub struct {
	key    string
	id     game.ID
	period store.Period
	since  time.Time
	ctx    context.Context
	cancel context.CancelFunc
	ready  chan struct{} // closed once the Redis subscription is up or failed
	err    error         // subscription error; read only after ready
	// members is guarded by broadcaster.mu.
	members map[*liveMember]struct{}
}

// liveMember is one socket. frames holds at most the newest frame, so a slow
// socket skips stale boards instead of holding up the others.
type liveMember struct {
	user   string
	frames chan []byte
	gone   chan struct{}
	once   sync.Once
}

func newBroadcaster(db BoardStore, cache LiveCache) *broadcaster {
	return &broadcaster{db: db, cache: cache, hubs: map[string]*boardHub{}}
}

func (m *liveMember) close() { m.once.Do(func() { close(m.gone) }) }

// offer replaces any frame the socket has not sent yet. Only the hub's
// goroutine sends, so after the drain the send cannot block.
func (m *liveMember) offer(payload []byte) {
	select {
	case m.frames <- payload:
		return
	default:
	}
	select {
	case <-m.frames:
	default:
	}
	select {
	case m.frames <- payload:
	default:
	}
}

// join registers a socket before its initial snapshot is read, so any change
// after that read reaches it through the hub.
func (b *broadcaster) join(id game.ID, period store.Period, key string, since time.Time, user string) (*boardHub, *liveMember, error) {
	member := &liveMember{user: user, frames: make(chan []byte, 1), gone: make(chan struct{})}
	b.mu.Lock()
	hub := b.hubs[key]
	if hub == nil {
		ctx, cancel := context.WithCancel(context.Background())
		hub = &boardHub{key: key, id: id, period: period, since: since, ctx: ctx, cancel: cancel,
			ready: make(chan struct{}), members: map[*liveMember]struct{}{}}
		b.hubs[key] = hub
		go b.run(hub)
	}
	hub.members[member] = struct{}{}
	b.mu.Unlock()
	<-hub.ready
	if hub.err != nil {
		b.leave(hub, member)
		return nil, nil, hub.err
	}
	return hub, member, nil
}

// leave removes a socket; the last one out closes the Redis subscription.
func (b *broadcaster) leave(hub *boardHub, member *liveMember) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(hub.members, member)
	if len(hub.members) == 0 && b.hubs[hub.key] == hub {
		delete(b.hubs, hub.key)
		hub.cancel()
	}
}

// stop ends a hub whose subscription closed and tells its sockets to close,
// so their clients reconnect, as each socket did before on its own.
func (b *broadcaster) stop(hub *boardHub) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hubs[hub.key] == hub {
		delete(b.hubs, hub.key)
	}
	hub.cancel()
	for member := range hub.members {
		member.close()
	}
}

func (b *broadcaster) run(hub *boardHub) {
	updates, closeSub, err := b.cache.SubscribeBoard(hub.ctx, hub.key)
	if err != nil {
		hub.err = err
		b.mu.Lock()
		if b.hubs[hub.key] == hub {
			delete(b.hubs, hub.key)
		}
		b.mu.Unlock()
		hub.cancel()
		close(hub.ready)
		return
	}
	close(hub.ready)
	defer closeSub()
	for {
		select {
		case <-hub.ctx.Done():
			return
		case _, ok := <-updates:
			if !ok {
				b.stop(hub)
				return
			}
			b.broadcast(hub)
		}
	}
}

// broadcast builds the shared top 20 once, looks up every member's rank in
// one batch, and hands each socket its frame.
func (b *broadcaster) broadcast(hub *boardHub) {
	b.mu.Lock()
	members := make([]*liveMember, 0, len(hub.members))
	seen := map[string]bool{}
	users := make([]string, 0, len(hub.members))
	for member := range hub.members {
		members = append(members, member)
		if member.user != "" && !seen[member.user] {
			seen[member.user] = true
			users = append(users, member.user)
		}
	}
	b.mu.Unlock()
	if len(members) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(hub.ctx, 5*time.Second)
	board, ranks, err := completeBoardRanks(ctx, b.db, b.cache, hub.id, hub.key, hub.since, users)
	cancel()
	if err != nil {
		// Neither Redis nor SQL could give a complete board. Close these
		// sockets rather than leave them showing a board that may be stale.
		for _, member := range members {
			member.close()
		}
		return
	}
	leaders, _ := json.Marshal(board.Leaders)
	shared := hubFrame(hub, leaders, nil)
	for _, member := range members {
		rank, ok := ranks[member.user]
		if member.user == "" || !ok {
			member.offer(shared)
			continue
		}
		member.offer(hubFrame(hub, leaders, &rank))
	}
}

// liveFrame has the same JSON shape as liveMessage; the leaders array is
// encoded once per change and reused for every socket.
type liveFrame struct {
	Type   string       `json:"type"`
	Game   game.ID      `json:"game"`
	Period store.Period `json:"period"`
	Board  struct {
		Leaders json.RawMessage `json:"leaders"`
		MyRank  *int64          `json:"myRank"`
	} `json:"board"`
}

func hubFrame(hub *boardHub, leaders json.RawMessage, rank *int64) []byte {
	frame := liveFrame{Type: "board", Game: hub.id, Period: hub.period}
	frame.Board.Leaders, frame.Board.MyRank = leaders, rank
	payload, _ := json.Marshal(frame)
	return payload
}
