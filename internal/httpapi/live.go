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
}

type liveMessage struct {
	Type   string       `json:"type"`
	Game   game.ID      `json:"game"`
	Period store.Period `json:"period"`
	Board  store.Board  `json:"board"`
}

func LiveHandler(db BoardStore, cache LiveCache, now func() time.Time) http.Handler {
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
		// Subscribe first; backfill and the initial snapshot then have no gap.
		updates, closeSub, err := cache.SubscribeBoard(r.Context(), key)
		if err != nil {
			failure(w, 503, "live board unavailable")
			return
		}
		defer closeSub()
		exists, err := cache.HasBoard(r.Context(), key)
		if err != nil {
			failure(w, 503, "live board unavailable")
			return
		}
		if !exists {
			rows, dbErr := db.BestScores(r.Context(), id, since)
			if dbErr != nil {
				failure(w, 503, "live board unavailable")
				return
			}
			if err = cache.Backfill(r.Context(), key, rows); err != nil {
				failure(w, 503, "live board unavailable")
				return
			}
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := conn.CloseRead(r.Context())
		send := func() error {
			board, err := cache.Board(ctx, key, user)
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(liveMessage{Type: "board", Game: id, Period: period, Board: board})
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return conn.Write(writeCtx, websocket.MessageText, payload)
		}
		if err = send(); err != nil {
			return
		}
		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-updates:
				if !ok {
					return
				}
				if err = send(); err != nil {
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
