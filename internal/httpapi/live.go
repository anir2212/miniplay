package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"miniplay/internal/game"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

type LiveCache interface {
	BoardCache
	SubscribeBoard(context.Context, string) (<-chan struct{}, func(), error)
	// Ranks returns one-based ranks for the given users that are on the
	// board; users not on it are left out.
	Ranks(context.Context, string, []string) (map[string]int64, error)
}

type liveMessage struct {
	Type   string       `json:"type"`
	Game   game.ID      `json:"game"`
	Period store.Period `json:"period"`
	Board  store.Board  `json:"board"`
}

// LiveHandler streams a board over a WebSocket: an initial frame, then a new
// frame after every change. Sockets on the same board share one broadcaster.
func LiveHandler(db BoardStore, cache LiveCache, now func() time.Time) http.Handler {
	hubs := newBroadcaster(db, cache)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			failure(w, 405, "method not allowed")
			return
		}
		id := game.ID(r.URL.Query().Get("game"))
		period := store.Period(r.URL.Query().Get("period"))
		if period == "" {
			period = store.All
		}
		user := r.URL.Query().Get("user")
		if user != "" && !token.ValidUser(user) {
			failure(w, 400, "invalid user")
			return
		}
		key, since, err := store.BoardKey(id, period, now())
		if err != nil {
			failure(w, 400, "invalid game or period")
			return
		}
		// Join the board's broadcaster first; the initial snapshot then has no
		// gap before the first update.
		hub, member, err := hubs.join(id, period, key, since, user)
		if err != nil {
			failure(w, 503, "live board unavailable")
			return
		}
		defer hubs.leave(hub, member)
		initial, err := completeBoard(r.Context(), db, cache, id, key, since, user)
		if err != nil {
			failure(w, 503, "live board unavailable")
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := conn.CloseRead(r.Context())
		write := func(payload []byte) error {
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return conn.Write(writeCtx, websocket.MessageText, payload)
		}
		// Each frame the hub builds re-checks the completeness marker, so a
		// frame after Redis key loss is never a partial board.
		payload, _ := json.Marshal(liveMessage{Type: "board", Game: id, Period: period, Board: initial})
		if err = write(payload); err != nil {
			return
		}
		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-member.gone:
				return
			case payload := <-member.frames:
				if err = write(payload); err != nil {
					return
				}
			case <-heartbeat.C:
				pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err = conn.Ping(pingCtx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	})
}
