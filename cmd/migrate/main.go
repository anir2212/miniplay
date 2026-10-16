package main

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5"
	"miniplay/internal/config"
	"miniplay/internal/migrate"
)

func main() {
	url, err := config.Required("DATABASE_URL")
	if err != nil {
		log.Fatal(err)
	}
	directory := config.Env("MIGRATION_DIR", "db/migrations")
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(ctx)
	count, err := migrate.Apply(ctx, conn, directory)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("applied %d migrations", count)
}
