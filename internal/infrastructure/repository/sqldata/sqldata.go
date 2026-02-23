package sqldata

import (
	"context"

	"github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqldata/types"
)

// Repo returns data (not metadata) from a SQL database.
// Implementations MUST NOT keep long-lived connections; open/close per call.
// All errors should be safe to show to end-user.
//
// FetchOne returns a single value from a column (LIMIT 1), optionally filtered by simple WHERE.
// If the selected value is an array (e.g. Postgres bytea[]), implementations should return the first element.
//
// FetchOneRaw returns one or many raw values from a column (LIMIT 1 row).
// For regular binary columns it's a slice with a single element.
// For array-of-binary columns (e.g. Postgres bytea[]) it returns all elements.
//
// Note: for now Where values are passed as strings and bound as parameters.
// This is enough for most key lookups.
//
// WhereJoin is either "AND" or "OR".
//
// NOTE: This file intentionally only defines the public API types. Driver implementations live in
// subpackages that should NOT import this package to avoid cycles. They should import `sqldata/types`.
type Repo interface {
	FetchOne(ctx context.Context, cfg Config, req FetchOneRequest) ([]byte, error)
	FetchOneRaw(ctx context.Context, cfg Config, req FetchOneRequest) ([][]byte, error)
}

type Config = types.Config

type WhereCond = types.WhereCond

type FetchOneRequest = types.FetchOneRequest
