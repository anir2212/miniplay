package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"miniplay/internal/config"
	"miniplay/internal/events"
	"miniplay/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	url, err := config.Required("DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	db, err := store.Open(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Pool.Close()
	kafka, err := events.NewKafka(config.Env("KAFKA_BROKERS", "127.0.0.1:9092"))
	if err != nil {
		log.Fatal(err)
	}
	defer kafka.Close()
	timer := time.NewTicker(500 * time.Millisecond)
	defer timer.Stop()
	for {
		count, err := events.PublishOnce(ctx, db, kafka, 100)
		if err != nil && ctx.Err() == nil {
			log.Printf("outbox retry: %v", err)
		}
		if count > 0 {
			log.Printf("published %d events", count)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
