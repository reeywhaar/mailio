package migrations

import (
	"context"
	"database/sql"
	"time"

	"mailio/internal/ids"
)

// A user has passwords rather than one: a phone's and a laptop's, each with a label and its own
// login, deleted one at a time. The password a user had becomes the one labelled "default",
// whose login is the plain address, so every client already set up keeps signing in. Only the
// hash is kept: a password is shown once, when it is made, and a device that needs one gets its
// own.
var userPasswords = Migration{
	Name: "20261109230324_user_passwords",
	Up: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE user_passwords (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  label        TEXT NOT NULL,
  -- The tag of its login: inbox+k3x9@example.org signs in with this password and no other. Empty
  -- for the plain address.
  login        TEXT NOT NULL,
  hash         TEXT NOT NULL,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER,
  UNIQUE (user_id, label),
  UNIQUE (user_id, login)
)`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, password_hash, created_at FROM users`)
		if err != nil {
			return err
		}
		type old struct {
			id, hash string
			created  int64
		}
		var users []old
		for rows.Next() {
			var o old
			if err := rows.Scan(&o.id, &o.hash, &o.created); err != nil {
				rows.Close()
				return err
			}
			users = append(users, o)
		}
		rows.Close()
		for _, o := range users {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO user_passwords (id, user_id, label, login, hash, created_at) VALUES (?, ?, 'default', '', ?, ?)`,
				ids.New("pw_", time.Now().UnixMilli()), o.id, o.hash, o.created); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `
ALTER TABLE users DROP COLUMN password_hash;
ALTER TABLE users DROP COLUMN password_sealed;
`)
		return err
	},
}
