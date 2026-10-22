package main

import (
	"context"
	"log"

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
	if httpapi.TrustedProxies, err = httpapi.ParseTrustedProxies(
		config.Env("MINIPLAY_TRUSTED_PROXIES", "127.0.0.0/8,::1/128")); err != nil {
		log.Fatal(err)
	}
	cache, err := store.OpenRedis(context.Background(), config.Env("REDIS_URL", "redis://127.0.0.1:6379/0"))
	if err != nil {
		log.Fatal(err)
	}
	defer cache.Client.Close()
	if err = config.Serve(config.LocalAddr("8081"), httpapi.SessionHandler(signer, cache)); err != nil {
		log.Fatal(err)
	}
}
