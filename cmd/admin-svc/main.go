package main

import (
	"context"
	"log"

	"miniplay/internal/config"
	"miniplay/internal/flags"
	"miniplay/internal/httpapi"
	"miniplay/internal/store"
)

func main() {
	token, err := config.AdminToken()
	if err != nil {
		log.Fatal(err)
	}
	if httpapi.TrustedProxies, err = httpapi.ParseTrustedProxies(
		config.Env("MINIPLAY_TRUSTED_PROXIES", "127.0.0.0/8,::1/128")); err != nil {
		log.Fatal(err)
	}
	flagTTL, err := flags.TTLFromEnv()
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
	handler := httpapi.AdminHandler(httpapi.AdminConfig{
		Token: token, Actor: config.Env("MINIPLAY_ADMIN_ACTOR", "local-demo-admin"), FlagTTL: flagTTL,
	}, db)
	if err = config.Serve(config.LocalAddr("8084"), handler); err != nil {
		log.Fatal(err)
	}
}
