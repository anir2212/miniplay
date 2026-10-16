package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"time"

	"miniplay/internal/abuse"
	"miniplay/internal/config"
	"miniplay/internal/store"
)

func main() {
	url, err := config.Required("DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Pool.Close()
	result, err := abuse.Scan(ctx, db, time.Now().UTC())
	if err != nil {
		log.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		log.Fatal(err)
	}
}
