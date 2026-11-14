// Package migrations is the schema, as an append-only list tracked by PRAGMA user_version.
//
// Go rather than .sql so a migration can move data with code when one needs to.
package migrations

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sort"
)

// Migration is one change. Name begins with the UTC moment the file was written,
// `date -u +%Y%m%d%H%M%S`, which is what orders it — see docs/conventions.md.
type Migration struct {
	Name string
	// Rebuild is a migration that recreates a table other tables reference. SQLite cannot change a
	// CHECK in place, and dropping the old table breaks every reference to it for a moment, so it
	// runs as SQLite's own procedure says: foreign keys off, and every reference checked before it
	// commits.
	Rebuild bool
	Up      func(context.Context, *sql.Tx) error
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
	messageLimits,
	catchAll,
	accountRelay,
	aliases,
	trashDays,
	junkDays,
	heldMail,
	scheduled,
	sendAs,
	receiveOnly,
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
	return run(ctx, db, "")
}

// RunBefore applies everything before the named migration and not it: the database as it was the
// moment before, for that migration's test.
func RunBefore(ctx context.Context, db *sql.DB, name string) error {
	for _, m := range all {
		if m.Name == name {
			return run(ctx, db, name)
		}
	}
	return fmt.Errorf("there is no migration %s", name)
}

func run(ctx context.Context, db *sql.DB, before string) error {
	list := append([]Migration(nil), all...)
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	if before != "" {
		list = list[:sort.Search(len(list), func(i int) bool { return list[i].Name >= before })]
	}

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
		if err := apply(ctx, db, list[i], i+1); err != nil {
			return fmt.Errorf("migrate %s: %w", list[i].Name, err)
		}
	}
	return nil
}

// apply runs one migration and stamps the version it brings the database to, on one connection
// throughout: a pragma is the connection's.
func apply(ctx context.Context, db *sql.DB, m Migration, version int) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if !m.Rebuild {
		return inTx(ctx, conn, m, version)
	}
	// Outside the transaction, where SQLite would ignore it, and on again whatever happened.
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	err = inTx(ctx, conn, m, version)
	if _, on := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); on != nil {
		// The connection goes back to the pool unchecked otherwise.
		conn.Raw(func(any) error { return driver.ErrBadConn })
		return errors.Join(err, on)
	}
	return err
}

func inTx(ctx context.Context, conn *sql.Conn, m Migration, version int) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := m.Up(ctx, tx); err != nil {
		return err
	}
	if m.Rebuild {
		if err := referencesHold(ctx, tx); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		return err
	}
	return tx.Commit()
}

// referencesHold refuses a rebuild that left a row pointing at nothing.
func referencesHold(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var (
			table, parent string
			rowid, fkid   sql.NullInt64
		)
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		return fmt.Errorf("a row of %s refers to a %s that is not there", table, parent)
	}
	return rows.Err()
}
