package migrations

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestMigrationFailureReportsOnlySafeDiagnostics(t *testing.T) {
	for _, code := range []string{"42501", "42P07", "42601"} {
		err := &pgconn.PgError{Code: code, Message: "private diagnostic", Detail: "private row"}
		got := migrationFailure("apply relay migration 0001.sql", err)
		if !strings.Contains(got.Error(), code) || !strings.Contains(got.Error(), "0001.sql") ||
			strings.Contains(got.Error(), "private") || errors.Unwrap(got) != nil {
			t.Errorf("unexpected migration diagnostic: %v", got)
		}
	}
}
