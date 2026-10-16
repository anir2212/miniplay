package main

import (
	"context"
	"log"
	"time"

	"miniplay/internal/config"
	"miniplay/internal/httpapi"
	"miniplay/internal/store"
	"miniplay/internal/token"
)

func main() {
	secret, err := config.Required("MINIPLAY_HMAC_SECRET")
	if err != nil {
		log.Fatal(err)
	}
	signer, err := token.New(secret, nil)
	if err != nil {
		log.Fatal(err)
	}
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
	handler := httpapi.ScoreHandler(signer, db, cache, func() int64 { return time.Now().UnixMilli() })
	if err = config.Serve(config.LocalAddr("8082"), handler); err != nil {
		log.Fatal(err)
	}
}
