package migrations

import (
	"context"
	"database/sql"
)

// A database made by the first v2 build has admins as flagged users and sessions keyed by user:
// the initial migration was changed in place after that build was deployed, which such a
// database never saw. This brings it to the same shape a fresh one has, and does nothing to a
// fresh one. Its flagged users stay users; with no admin yet, serve logs an admin link.
var adminsApart = Migration{
	Name: "20261109041411_admins_apart",
	Up: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS admins (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at    INTEGER NOT NULL
)`); err != nil {
			return err
		}
		var keyedByUser int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM pragma_table_info('sessions') WHERE name = 'user_id'`).Scan(&keyedByUser); err != nil {
			return err
		}
		if keyedByUser == 0 {
			return nil
		}
		// Those sessions belong to users who can no longer sign in here, so none is worth keeping.
		_, err := tx.ExecContext(ctx, `
DROP TABLE sessions;
CREATE TABLE sessions (
  id_hash      BLOB PRIMARY KEY,
  admin_id     TEXT NOT NULL REFERENCES admins (id) ON DELETE CASCADE,
  user_agent   TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL
);
CREATE INDEX sessions_admin ON sessions (admin_id);
CREATE INDEX sessions_expiry ON sessions (expires_at);
`)
		return err
	},
}
