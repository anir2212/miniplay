package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/twmb/franz-go/pkg/kgo"
	"miniplay/internal/config"
	"miniplay/internal/events"
	"miniplay/internal/store"
)

func run(ctx context.Context) error {
	url, err := config.Required("DATABASE_URL")
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	boards, err := store.OpenRedis(ctx, config.Env("REDIS_URL", "redis://127.0.0.1:6379/0"))
	if err != nil {
		return err
	}
	defer boards.Client.Close()
	brokers := strings.Split(config.Env("KAFKA_BROKERS", "127.0.0.1:9092"), ",")
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(events.Topic), kgo.ConsumerGroup("miniplay-stats"), kgo.DisableAutoCommit())
	if err != nil {
		return err
	}
	defer client.Close()
	producer := &events.Kafka{Client: client}
	for ctx.Err() == nil {
		fetches := client.PollFetches(ctx)
		for _, fetchError := range fetches.Errors() {
			if ctx.Err() == nil {
				return fetchError.Err
			}
		}
		var processingError error
		fetches.EachRecord(func(record *kgo.Record) {
			if processingError != nil {
				return
			}
			_, processingError = events.Process(ctx, record.Value, db, boards, producer)
			if processingError == nil {
				processingError = client.CommitRecords(ctx, record)
			}
		})
		if processingError != nil {
			return fmt.Errorf("worker stopped before offset commit: %w", processingError)
		}
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		log.Fatal(err)
	}
}
