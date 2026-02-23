package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	_ "github.com/go-sql-driver/mysql"

	sqlmeta "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqlmeta"
)

type Repo struct {
	ConnectTimeout time.Duration
}

func New() *Repo {
	return &Repo{ConnectTimeout: 3 * time.Second}
}

func (r *Repo) Ping(ctx context.Context, cfg sqlmeta.Config) error {
	db, err := r.open(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ctx, cancel := context.WithTimeout(ctx, r.ConnectTimeout)
	defer cancel()
	return db.PingContext(ctx)
}

func (r *Repo) Schemas(ctx context.Context, cfg sqlmeta.Config) ([]string, error) {
	db, err := r.open(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	// In MySQL, "schemas" are databases.
	builder := sq.StatementBuilder.PlaceholderFormat(sq.Question)
	q := builder.Select("schema_name").From("information_schema.schemata").OrderBy("schema_name")
	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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
	db, err := r.open(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	schema = strings.TrimSpace(schema)
	if schema == "" {
		return nil, fmt.Errorf("schema is required")
	}

	builder := sq.StatementBuilder.PlaceholderFormat(sq.Question)
	q := builder.
		Select("table_name").
		From("information_schema.tables").
		Where(sq.Eq{"table_schema": schema}).
		Where(sq.Expr("table_type in ('BASE TABLE','VIEW')")).
		OrderBy("table_name")

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

func (r *Repo) Columns(ctx context.Context, cfg sqlmeta.Config, schema, table string) ([]string, error) {
	db, err := r.open(cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()

	schema = strings.TrimSpace(schema)
	table = strings.TrimSpace(table)
	if schema == "" || table == "" {
		return nil, fmt.Errorf("schema and table are required")
	}

	builder := sq.StatementBuilder.PlaceholderFormat(sq.Question)
	q := builder.
		Select("column_name").
		From("information_schema.columns").
		Where(sq.Eq{"table_schema": schema, "table_name": table}).
		OrderBy("ordinal_position")

	sqlStr, args, err := q.ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

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

func (r *Repo) open(cfg sqlmeta.Config) (*sql.DB, error) {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return nil, fmt.Errorf("sql host is empty")
	}
	if cfg.Port <= 0 {
		return nil, fmt.Errorf("sql port is invalid")
	}

	user := strings.TrimSpace(cfg.User)
	dbName := strings.TrimSpace(cfg.DB)
	if dbName == "" {
		dbName = "mysql"
	}

	tlsVal := "false"
	if cfg.TLS {
		tlsVal = "preferred"
	}
	addr := fmt.Sprintf("%s:%d", host, cfg.Port)
	cred := user
	if user != "" {
		cred = fmt.Sprintf("%s:%s", user, cfg.Password)
	}
	if cred != "" {
		cred += "@"
	}
	dsn := fmt.Sprintf("%stcp(%s)/%s?parseTime=true&tls=%s", cred, addr, dbName, tlsVal)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetConnMaxIdleTime(30 * time.Second)
	db.SetMaxIdleConns(2)
	return db, nil
}
