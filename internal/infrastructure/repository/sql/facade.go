package sqlrepo

import (
	"context"
	"fmt"
	"strings"

	mysqlrepo "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sql/mysql"
	postgresrepo "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sql/postgres"
	sqlmeta "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqlmeta"
)

const (
	DriverPostgres = "postgres"
	DriverMySQL    = "mysql"
)

// New returns a driver-specific repo implementation.
func New(driver string) (sqlmeta.Repo, error) {
	d := strings.ToLower(strings.TrimSpace(driver))
	switch d {
	case DriverPostgres:
		return postgresrepo.New(), nil
	case DriverMySQL:
		return mysqlrepo.New(), nil
	default:
		return nil, fmt.Errorf("unsupported SQL driver: %s", driver)
	}
}

// Helpers to keep call sites short.
func Ping(ctx context.Context, driver string, cfg sqlmeta.Config) error {
	r, err := New(driver)
	if err != nil {
		return err
	}
	return r.Ping(ctx, cfg)
}

func Schemas(ctx context.Context, driver string, cfg sqlmeta.Config) ([]string, error) {
	r, err := New(driver)
	if err != nil {
		return nil, err
	}
	return r.Schemas(ctx, cfg)
}

func Tables(ctx context.Context, driver string, cfg sqlmeta.Config, schema string) ([]string, error) {
	r, err := New(driver)
	if err != nil {
		return nil, err
	}
	return r.Tables(ctx, cfg, schema)
}

func Columns(ctx context.Context, driver string, cfg sqlmeta.Config, schema, table string) ([]string, error) {
	r, err := New(driver)
	if err != nil {
		return nil, err
	}
	return r.Columns(ctx, cfg, schema, table)
}
