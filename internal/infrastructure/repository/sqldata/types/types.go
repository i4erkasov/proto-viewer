package types

// This package contains shared types for sqldata repositories.
//
// It exists purely to avoid cyclic imports between:
// - sqldata (facade/API)
// - sqldata/postgres, sqldata/mysql (driver implementations)
//
// Driver packages should import this package, not `sqldata`.

type Config struct {
	Host     string
	Port     int
	TLS      bool
	User     string
	Password string
	DB       string
}

type WhereCond struct {
	Field string // column name
	Op    string // =, !=, >, >=, <, <=, like
	Value string // bound as parameter
}

type FetchOneRequest struct {
	Schema string
	Table  string
	Column string

	WhereJoin string
	Where     []WhereCond
}
