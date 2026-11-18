package collections

import "github.com/home-atlas/homeatlas/internal/core/collections/adapters/sqlite"

func querySQL(c Config, q string) string { return sqlite.Query(c, q) }
