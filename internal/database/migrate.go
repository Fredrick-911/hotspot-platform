package database

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationLockID is an arbitrary number that identifies "a migration is
// running" to PostgreSQL, so two API instances starting together cannot both
// run the same file.
const migrationLockID = 727274

// Migrate applies every *.sql file in files that has not been applied yet,
// in file-name order. Each file runs in its own transaction and is recorded
// in the schema_migrations table, so running Migrate again does nothing.
//
// Files are plain SQL (no goose-style Up/Down markers). Never edit a file that
// has been released; add a new one.
func Migrate(ctx context.Context, db *pgxpool.Pool, files fs.FS, logger *slog.Logger) error {
	if _, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.Glob(files, "*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)

	for _, name := range entries {
		version := strings.TrimSuffix(name, ".sql")

		body, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		applied, err := applyOne(ctx, db, version, string(body))
		if err != nil {
			return fmt.Errorf("migration %s failed: %w", name, err)
		}
		if applied {
			logger.Info("migration applied", "version", version)
		}
	}
	return nil
}

func applyOne(ctx context.Context, db *pgxpool.Pool, version, body string) (bool, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Held until this transaction ends.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return false, err
	}

	var already bool
	if err := tx.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version,
	).Scan(&already); err != nil {
		return false, err
	}
	if already {
		return false, nil
	}

	// No arguments => pgx uses the simple protocol, which allows many statements.
	if _, err := tx.Exec(ctx, body); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
