package sqldata

import (
	"fmt"
	"strings"
	"time"

	mysqlrepo "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqldata/mysql"
	postgresrepo "github.com/i4erkasov/proto-viewer/internal/infrastructure/repository/sqldata/postgres"
)

const (
	DriverPostgres = "postgres"
	DriverMySQL    = "mysql"
)

func New(driver string) (Repo, error) {
	d := strings.ToLower(strings.TrimSpace(driver))
	switch d {
	case DriverPostgres:
		return newPostgres(), nil
	case DriverMySQL:
		return newMySQL(), nil
	default:
		return nil, fmt.Errorf("unsupported SQL driver: %s", driver)
	}
}

const defaultConnectTimeout = 3 * time.Second

func newPostgres() *postgresrepo.Repo {
	return &postgresrepo.Repo{ConnectTimeout: defaultConnectTimeout}
}
func newMySQL() *mysqlrepo.Repo { return &mysqlrepo.Repo{ConnectTimeout: defaultConnectTimeout} }
