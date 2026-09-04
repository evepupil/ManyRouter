package migrations

import "embed"

// Files contains the versioned ManyRouter database migrations.
//
//go:embed *.sql
var Files embed.FS
