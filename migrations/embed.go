// Package migrations holds the SQL files that build our own tables
// (tenants, plans, payments, sessions, users...). They are compiled into the
// API binary and applied at startup by internal/database.Migrate.
package migrations

import "embed"

// Files are applied in file-name order, each exactly once.
//
//go:embed *.sql
var Files embed.FS
