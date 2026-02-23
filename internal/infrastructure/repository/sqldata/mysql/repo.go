package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	_ "github.com/go-sql-driver/mysql"

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
		return nil, sql.ErrNoRows
	}
	return vals[0], nil
}

func (r *Repo) FetchOneRaw(ctx context.Context, cfg sqldatatypes.Config, req sqldatatypes.FetchOneRequest) ([][]byte, error) {
	db, err := r.open(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	schema := strings.TrimSpace(req.Schema)
	table := strings.TrimSpace(req.Table)
	col := strings.TrimSpace(req.Column)
	if schema == "" || table == "" || col == "" {
		return nil, fmt.Errorf("schema, table and column are required")
	}

	builder := sq.StatementBuilder.PlaceholderFormat(sq.Question)
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

	var b []byte
	if err := db.QueryRowContext(ctx, sqlStr, args...).Scan(&b); err != nil {
		return nil, err
	}
	return [][]byte{b}, nil
}

func (r *Repo) open(cfg sqldatatypes.Config) (*sql.DB, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, fmt.Errorf("sql host is empty")
	}
	if cfg.Port <= 0 {
		return nil, fmt.Errorf("sql port is invalid")
	}
	// In mysql driver DSN, DB is optional but recommended.
	dbName := strings.TrimSpace(cfg.DB)

	user := strings.TrimSpace(cfg.User)
	pass := cfg.Password
	if user == "" {
		user = "root"
	}

	params := "parseTime=true"
	if cfg.TLS {
		// Rely on system certs. User can configure mysql tls config name later if needed.
		params += "&tls=true"
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?%s", user, pass, host, cfg.Port, dbName, params)
	return sql.Open("mysql", dsn)
}

func quoteIdent(s string) string {
	// MySQL ident quoting using backticks.
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "`", "``")
	return "`" + s + "`"
}
