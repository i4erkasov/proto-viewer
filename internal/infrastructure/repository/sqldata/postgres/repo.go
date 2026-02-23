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

	sqldatatypes "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqldata/types"
)

type Repo struct {
	ConnectTimeout time.Duration
}

func (r *Repo) FetchOne(ctx context.Context, cfg sqldatatypes.Config, req sqldatatypes.FetchOneRequest) ([]byte, error) {
	vals, err := r.FetchOneRaw(ctx, cfg, req)
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, fmt.Errorf("no rows")
	}
	return vals[0], nil
}

func (r *Repo) FetchOneRaw(ctx context.Context, cfg sqldatatypes.Config, req sqldatatypes.FetchOneRequest) ([][]byte, error) {
	pool, err := r.open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	schema := strings.TrimSpace(req.Schema)
	table := strings.TrimSpace(req.Table)
	col := strings.TrimSpace(req.Column)
	if schema == "" || table == "" || col == "" {
		return nil, fmt.Errorf("schema, table and column are required")
	}

	builder := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	q := builder.Select(quoteIdent(col)).From(fmt.Sprintf("%s.%s", quoteIdent(schema), quoteIdent(table))).Limit(1)

	join := strings.ToUpper(strings.TrimSpace(req.WhereJoin))
	if join == "" {
		join = "AND"
	}
	if join != "AND" && join != "OR" {
		return nil, fmt.Errorf("invalid where join: %s", req.WhereJoin)
	}

	parts := make([]sq.Sqlizer, 0, len(req.Where))
	for _, c := range req.Where {
		field := strings.TrimSpace(c.Field)
		if field == "" {
			continue
		}
		op := strings.TrimSpace(strings.ToLower(c.Op))
		if op == "" {
			op = "="
		}
		switch op {
		case "=", "!=", ">", ">=", "<", "<=", "like":
		default:
			return nil, fmt.Errorf("unsupported operator: %s", c.Op)
		}

		parts = append(parts, sq.Expr(fmt.Sprintf("%s %s ?", quoteIdent(field), op), c.Value))
	}

	if len(parts) > 0 {
		if join == "AND" {
			q = q.Where(sq.And(parts))
		} else {
			q = q.Where(sq.Or(parts))
		}
	}

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	timeout := r.ConnectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Postgres can store binary protobuf as:
	// - BYTEA (scans into []byte)
	// - BYTEA[] (scans into [][]byte) -- OID 1001 (_bytea)
	// We support both and always return a slice of blobs.
	var b []byte
	err = pool.QueryRow(ctx, sqlStr, args...).Scan(&b)
	if err == nil {
		return [][]byte{b}, nil
	}

	var arr [][]byte
	if err2 := pool.QueryRow(ctx, sqlStr, args...).Scan(&arr); err2 == nil {
		if len(arr) == 0 {
			return nil, fmt.Errorf("no rows")
		}
		return arr, nil
	}

	return nil, err
}

func (r *Repo) open(ctx context.Context, cfg sqldatatypes.Config) (*pgxpool.Pool, error) {
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

	if cfg.TLS {
		if config.ConnConfig.TLSConfig == nil {
			config.ConnConfig.TLSConfig = &tls.Config{}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, r.ConnectTimeout)
	defer cancel()
	return pgxpool.NewWithConfig(ctx, config)
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

func quoteIdent(s string) string {
	// Minimal quoting for identifiers (schema/table/column). Double up quotes.
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\"", "\"\"")
	return "\"" + s + "\""
}
