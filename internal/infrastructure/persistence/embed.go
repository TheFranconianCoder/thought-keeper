package persistence

import "embed"

// migrationsFS embeds the goose migrations so the binary is self-contained.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS
