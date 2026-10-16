package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Apply runs each numbered SQL file once and records its version in the same
// transaction. A session advisory lock prevents two migrators from racing.
func Apply(ctx context.Context, conn *pgx.Conn, directory string) (int, error) {
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(47211011)`); err != nil {
		return 0, err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock(47211011)`)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version TEXT PRIMARY KEY,applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, err
	}
	names := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	applied := 0
	for _, name := range names {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, err
		}
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&exists)
		if err != nil {
			tx.Rollback(ctx)
			return applied, err
		}
		if exists {
			tx.Rollback(ctx)
			continue
		}
		contents, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			tx.Rollback(ctx)
			return applied, err
		}
		for _, statement := range strings.Split(string(contents), ";") {
			statement = strings.TrimSpace(statement)
			if statement == "" {
				continue
			}
			if _, err = tx.Exec(ctx, statement); err != nil {
				tx.Rollback(ctx)
				return applied, fmt.Errorf("migration %s: %w", name, err)
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name); err != nil {
			tx.Rollback(ctx)
			return applied, err
		}
		if err = tx.Commit(ctx); err != nil {
			return applied, err
		}
		applied++
	}
	return applied, nil
}
