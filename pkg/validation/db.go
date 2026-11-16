package validation

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/golaravel/golaravel/pkg/database"
)

// SQLChecker answers unique / exists rules with COUNT queries on db. Table
// and column names come from your rule strings and are checked to be plain
// identifiers.
func SQLChecker(db *sql.DB, dialect database.Dialect) DBChecker {
	return sqlChecker{db: db, d: dialect}
}

type sqlChecker struct {
	db *sql.DB
	d  database.Dialect
}

func (s sqlChecker) Count(ctx context.Context, table, column string, value any, ignoreColumn string, ignoreValue any) (int64, error) {
	if !database.ValidIdentifier(table) || !database.ValidIdentifier(column) || (ignoreColumn != "" && !database.ValidIdentifier(ignoreColumn)) {
		return 0, fmt.Errorf("validation: invalid table or column in rule (%q.%q)", table, column)
	}
	q := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, column)
	args := []any{value}
	if ignoreColumn != "" {
		q += fmt.Sprintf(" AND %s <> ?", ignoreColumn)
		args = append(args, ignoreValue)
	}
	var n int64
	err := s.db.QueryRowContext(ctx, s.d.Rebind(q), args...).Scan(&n)
	return n, err
}
