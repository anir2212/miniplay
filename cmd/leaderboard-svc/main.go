package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"miniplay/internal/config"
	"miniplay/internal/httpapi"
	"miniplay/internal/store"
)

func main() {
	url, err := config.Required("DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(context.Background(), url)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Pool.Close()
	cache, err := store.OpenRedis(context.Background(), config.Env("REDIS_URL", "redis://127.0.0.1:6379/0"))
	if err != nil {
		log.Fatal(err)
	}
	defer cache.Client.Close()
	mux := http.NewServeMux()
	mux.Handle("/api/leaderboards", httpapi.LeaderboardHandler(db, cache, time.Now))
	mux.Handle("/api/boards/live", httpapi.LiveHandler(db, cache, time.Now))
	mux.Handle("/health", httpapi.LeaderboardHandler(db, cache, time.Now))
	if err = config.Serve(config.LocalAddr("8083"), mux); err != nil {
		log.Fatal(err)
	}
}
