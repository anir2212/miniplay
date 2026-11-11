// Command portal-check-backend exists only for scripts/admin_portal_check.sh. It
// serves the real session, score and leaderboard handlers against a real local
// PostgreSQL, with an in-memory stand-in where Redis would be: every rate-limit
// check passes, board writes are dropped, and board reads fall back to SQL. It
// lets the portal be checked in a browser on a machine with no Redis. It refuses
// to start unless MINIPLAY_PORTAL_CHECK=1 and the database is on loopback, and it
// must never be deployed: it has no rate limits.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"miniplay/internal/config"
	"miniplay/internal/flags"
	"miniplay/internal/game"
	"miniplay/internal/httpapi"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

// noRedis stands in for Redis. Reads fail, which sends boards to the SQL path.
type noRedis struct{}

func (noRedis) Allow(context.Context, string, int, float64) (bool, error) { return true, nil }
func (noRedis) UpdateBoards(context.Context, store.Score) error           { return nil }
func (noRedis) HasBoard(context.Context, string) (bool, error) {
	return false, errors.New("no Redis in the portal check")
}
func (noRedis) Backfill(context.Context, string, []store.Score) error {
	return errors.New("no Redis in the portal check")
}
func (noRedis) Board(context.Context, string, string) (store.Board, error) {
	return store.Board{}, errors.New("no Redis in the portal check")
}

func requireCheckMode(raw string) error {
	if os.Getenv("MINIPLAY_PORTAL_CHECK") != "1" {
		return errors.New("this is a test harness with no rate limits; set MINIPLAY_PORTAL_CHECK=1 to run it")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(parsed.Hostname()); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("database host %q is not a loopback address", parsed.Hostname())
	}
	return nil
}

// seed writes clearly synthetic accepted runs, so the real abuse-scan has enough
// to fit a model. It stores them through the real attempt path, then applies their
// events through the real SQL aggregate. There is no Kafka in this path.
func seed(ctx context.Context, db *store.Postgres, perGame int) (int, error) {
	seeded := 0
	for _, id := range []game.ID{game.Rhythm, game.Reflex} {
		for i := range perGame {
			raw := make([]byte, 12)
			if _, err := rand.Read(raw); err != nil {
				return seeded, err
			}
			runID := "synthetic" + hex.EncodeToString(raw)
			targets, err := game.Targets(id, runID)
			if err != nil {
				return seeded, err
			}
			hits, user := 5+i%4, fmt.Sprintf("synthetic-%02d", i)
			if i == 0 { // one run far above the rest, so the scan has something to raise
				hits, user = len(targets), "synthetic-high-scorer"
			}
			taps := make([]game.Tap, 0, hits)
			for _, target := range targets[:hits] {
				taps = append(taps, game.Tap{MS: target.MS, X: target.X, Y: target.Y})
			}
			base, err := game.Replay(id, runID, taps)
			if err != nil {
				return seeded, err
			}
			row := store.Score{RunID: runID, User: user, Game: id, Score: base, BaseScore: base, Multiplier: 1, FlagVersion: 1, DurationMS: game.RunMilliseconds}
			if _, err = db.RecordAttempt(ctx, row, taps, ""); err != nil {
				return seeded, err
			}
			seeded++
		}
	}
	rows, err := db.Pending(ctx, 500)
	if err != nil {
		return seeded, err
	}
	for _, row := range rows {
		event, err := store.DecodeEvent(row.Payload)
		if err != nil {
			return seeded, err
		}
		if _, err = db.ApplyEvent(ctx, event); err != nil {
			return seeded, err
		}
	}
	return seeded, nil
}

func main() {
	seedRuns := flag.Int("seed", 0, "write this many synthetic runs per game, then exit")
	flag.Parse()
	databaseURL, err := config.Required("DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	if err = requireCheckMode(databaseURL); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Pool.Close()
	if *seedRuns > 0 {
		count, err := seed(ctx, db, *seedRuns)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("seeded %d synthetic runs", count)
		return
	}
	secret, err := config.Required("MINIPLAY_HMAC_SECRET")
	if err != nil {
		log.Fatal(err)
	}
	signer, err := token.New(secret, nil)
	if err != nil {
		log.Fatal(err)
	}
	ttl, err := flags.TTLFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	cache, err := flags.NewCache(db, ttl, nil)
	if err != nil {
		log.Fatal(err)
	}
	stand := noRedis{}
	mux := http.NewServeMux()
	mux.Handle("/api/sessions", httpapi.SessionHandler(signer, stand))
	mux.Handle("/api/scores", httpapi.ScoreHandler(signer, db, cache, stand, func() int64 { return time.Now().UnixMilli() }))
	mux.Handle("/api/leaderboards", httpapi.LeaderboardHandler(db, stand, time.Now))
	if err = config.Serve(config.LocalAddr("18081"), mux); err != nil {
		log.Fatal(err)
	}
}
