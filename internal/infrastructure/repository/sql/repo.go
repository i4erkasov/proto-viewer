package sqlrepo

import "context"

// Repo is a common interface for SQL metadata operations.
// Each SQL driver has its own implementation.
//
// Contract:
// - All errors must be safe to show to end-users (English).
// - Methods should return stable, sorted lists where reasonable.
// - Implementations must NOT keep long-lived connections inside; callers manage connect/disconnect.
type Repo interface {
	Ping(ctx context.Context, cfg Config) error
	Schemas(ctx context.Context, cfg Config) ([]string, error)
	Tables(ctx context.Context, cfg Config, schema string) ([]string, error)
	Columns(ctx context.Context, cfg Config, schema, table string) ([]string, error)
}

type Config struct {
	Host     string
	Port     int
	TLS      bool
	User     string
	Password string
	DB       string
}
