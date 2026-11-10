package migrations

import (
	"context"
	"database/sql"
	"time"

	"mailio/internal/ids"
)

// Server-wide defaults, and each domain's overrides of them: what happens to mail for addresses
// nobody has, its DMARC policy, the ending of its SPF, and the relay its mail goes through.
// Relays become a list with labels, and the one relay there was is the first of them and the
// default.
var defaults = Migration{
	Name: "20261109233510_defaults",
	Up: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE relays (
  id              TEXT PRIMARY KEY,
  label           TEXT NOT NULL UNIQUE COLLATE NOCASE,
  host            TEXT NOT NULL,
  port            INTEGER NOT NULL,
  security        TEXT NOT NULL CHECK (security IN ('starttls', 'implicit')),
  username        TEXT NOT NULL DEFAULT '',
  -- Sealed with MAILIO_SECRET_KEY to the relay's id; to the old single relay's aad while
  -- legacy_aad is set, until it is next saved.
  password_sealed BLOB,
  legacy_aad      INTEGER NOT NULL DEFAULT 0,
  created_at      INTEGER NOT NULL
);

-- One row. Reject, because everything mailio sends carries the domain's DKIM signature; ~all,
-- which is what the Postfix-based mailio published. A null relay is built in: each recipient's
-- MX, directly.
CREATE TABLE settings (
  id              INTEGER PRIMARY KEY CHECK (id = 1),
  unknown         TEXT NOT NULL DEFAULT 'reject' CHECK (unknown IN ('reject', 'drop', 'junk')),
  unknown_user_id TEXT REFERENCES users (id),
  dmarc           TEXT NOT NULL DEFAULT 'reject' CHECK (dmarc IN ('none', 'quarantine', 'reject')),
  spf             TEXT NOT NULL DEFAULT '~all' CHECK (spf IN ('-all', '~all', '?all')),
  relay_id        TEXT REFERENCES relays (id),
  CHECK ((unknown = 'junk') = (unknown_user_id IS NOT NULL))
);
INSERT INTO settings (id) VALUES (1);

-- Whether a domain's unknown and unknown_user_id are its own, or it follows the default. A
-- domain that does anything but reject keeps doing it.
ALTER TABLE domains ADD COLUMN unknown_own INTEGER NOT NULL DEFAULT 0;
UPDATE domains SET unknown_own = 1 WHERE unknown <> 'reject';

-- A domain's own; null follows the default. send_via 'builtin' delivers directly, 'relay' goes
-- through relay_id.
ALTER TABLE domains ADD COLUMN dmarc TEXT CHECK (dmarc IN ('none', 'quarantine', 'reject'));
ALTER TABLE domains ADD COLUMN spf TEXT CHECK (spf IN ('-all', '~all', '?all'));
ALTER TABLE domains ADD COLUMN send_via TEXT CHECK (send_via IN ('builtin', 'relay'));
ALTER TABLE domains ADD COLUMN relay_id TEXT REFERENCES relays (id);
`); err != nil {
			return err
		}
		var (
			host, security, username string
			port                     int
			sealed                   []byte
		)
		err := tx.QueryRowContext(ctx, `SELECT host, port, security, username, password_sealed FROM relay WHERE id = 1`).
			Scan(&host, &port, &security, &username, &sealed)
		switch {
		case err == sql.ErrNoRows:
		case err != nil:
			return err
		default:
			now := time.Now()
			id := ids.New(ids.Relay, now.UnixMilli())
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO relays (id, label, host, port, security, username, password_sealed, legacy_aad, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`,
				id, host, host, port, security, username, sealed, now.Unix()); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE settings SET relay_id = ? WHERE id = 1`, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `DROP TABLE relay`)
		return err
	},
}
