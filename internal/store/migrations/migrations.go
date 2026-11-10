// Package migrations is the schema, as an append-only list tracked by PRAGMA user_version.
//
// Go rather than .sql so a migration can move data with code when one needs to.
package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// Migration is one change. Name begins with the UTC moment the file was written,
// `date -u +%Y%m%d%H%M%S`, which is what orders it — see docs/conventions.md.
type Migration struct {
	Name string
	Up   func(context.Context, *sql.Tx) error
}

// all is the declared order, which is documentation; the sort below is what decides.
var all = []Migration{
	initialSchema,
	mailboxes,
	certificates,
	sealedPasswords,
	sending,
	adminsApart,
	userPasswords,
	incoming,
	defaults,
	accounts,
	junkPerDomain,
}

func exec(sqlText string) func(context.Context, *sql.Tx) error {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, sqlText)
		return err
	}
}

// Run applies everything not yet applied.
//
// Each migration runs in its own transaction that also stamps the version, so a failure leaves
// the database at the last version that fully applied.
func Run(ctx context.Context, db *sql.DB) error {
	list := append([]Migration(nil), all...)
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	// A version ahead of the build cannot be reasoned about: this binary does not know which of
	// its statements are still correct.
	if version > len(list) {
		return fmt.Errorf("database is at schema version %d and this build knows %d; it was written by a newer mailio", version, len(list))
	}

	for i := version; i < len(list); i++ {
		m := list[i]
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migrate %s: %w", m.Name, err)
		}
		if err := m.Up(ctx, tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate %s: %w", m.Name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate %s: %w", m.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate %s: %w", m.Name, err)
		}
	}
	return nil
}
