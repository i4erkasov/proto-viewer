package postgres

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlmeta "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqlmeta"
)

type Repo struct {
	ConnectTimeout time.Duration
}

func New() *Repo {
	return &Repo{ConnectTimeout: 3 * time.Second}
}

func (r *Repo) Ping(ctx context.Context, cfg sqlmeta.Config) error {
	pool, err := r.open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(ctx, r.ConnectTimeout)
	defer cancel()
	return pool.Ping(ctx)
}

func (r *Repo) Schemas(ctx context.Context, cfg sqlmeta.Config) ([]string, error) {
	pool, err := r.open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	builder := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	q := builder.Select("schema_name").From("information_schema.schemata").OrderBy("schema_name")
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		res = append(res, s)
	}
	return res, rows.Err()
}

func (r *Repo) Tables(ctx context.Context, cfg sqlmeta.Config, schema string) ([]string, error) {
	pool, err := r.open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	schema = strings.TrimSpace(schema)
	if schema == "" {
		return nil, fmt.Errorf("schema is required")
	}

	builder := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	q := builder.
		Select("table_name").
		From("information_schema.tables").
		Where(sq.Eq{"table_schema": schema}).
		Where(sq.Eq{"table_type": "BASE TABLE"}).
		OrderBy("table_name")

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		res = append(res, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return res, nil
}

func (r *Repo) Columns(ctx context.Context, cfg sqlmeta.Config, schema, table string) ([]string, error) {
	pool, err := r.open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	schema = strings.TrimSpace(schema)
	table = strings.TrimSpace(table)
	if schema == "" || table == "" {
		return nil, fmt.Errorf("schema and table are required")
	}

	builder := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	q := builder.
		Select("column_name").
		From("information_schema.columns").
		Where(sq.Eq{"table_schema": schema, "table_name": table}).
		OrderBy("ordinal_position")

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		res = append(res, s)
	}
	return res, rows.Err()
}

func (r *Repo) open(ctx context.Context, cfg sqlmeta.Config) (*pgxpool.Pool, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, fmt.Errorf("sql host is empty")
	}
	if cfg.Port <= 0 {
		return nil, fmt.Errorf("sql port is invalid")
	}
	// DB is required for pgx connection string; but postgres has a default.
	dbName := strings.TrimSpace(cfg.DB)
	if dbName == "" {
		dbName = "postgres"
	}

	sslMode := "disable"
	if cfg.TLS {
		sslMode = "require"
	}

	connStr := fmt.Sprintf("postgres://%s@%s:%d/%s?sslmode=%s", urlUser(cfg.User, cfg.Password), host, cfg.Port, dbName, sslMode)
	config, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, err
	}

	// If TLS is enabled, pgx uses standard TLS config.
	if cfg.TLS {
		if config.ConnConfig.TLSConfig == nil {
			config.ConnConfig.TLSConfig = &tls.Config{}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, r.ConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	return pool, nil
}

func urlUser(user, pass string) string {
	user = strings.TrimSpace(user)
	if user == "" {
		user = "postgres"
	}
	if pass == "" {
		return url.QueryEscape(user)
	}
	return fmt.Sprintf("%s:%s", url.QueryEscape(user), url.QueryEscape(pass))
}
