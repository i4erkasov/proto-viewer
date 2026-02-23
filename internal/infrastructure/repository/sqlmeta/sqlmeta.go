package sqlmeta

import "context"

// Repo is a common interface for SQL metadata operations.
// Each SQL driver has its own implementation.
//
// This package lives outside driver impl packages to avoid cyclic imports.
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
