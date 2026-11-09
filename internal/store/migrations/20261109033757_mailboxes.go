package migrations

import (
	"context"
	"database/sql"
	"time"
)

// Mailboxes and the messages in them: what IMAP serves and SMTP delivers into. Every existing user
// is given the standard set.
var mailboxes = Migration{
	Name: "20261109033757_mailboxes",
	Up: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
CREATE TABLE mailboxes (
  id           INTEGER PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  -- "INBOX" exactly; every other name as the client wrote it, "/" separating levels.
  name         TEXT NOT NULL,
  -- An IMAP special-use attribute, "\Sent" or "\Junk", or empty.
  special_use  TEXT NOT NULL DEFAULT '',
  subscribed   INTEGER NOT NULL DEFAULT 1,
  uid_validity INTEGER NOT NULL,
  uid_next     INTEGER NOT NULL DEFAULT 1,
  UNIQUE (user_id, name)
);

CREATE TABLE messages (
  id          INTEGER PRIMARY KEY,
  mailbox_id  INTEGER NOT NULL REFERENCES mailboxes (id) ON DELETE CASCADE,
  uid         INTEGER NOT NULL,
  -- Space-separated, sorted: "\Seen \Flagged $Junk".
  flags       TEXT NOT NULL DEFAULT '',
  -- IMAP's INTERNALDATE: when it arrived, not the Date header.
  received_at INTEGER NOT NULL,
  size        INTEGER NOT NULL,
  UNIQUE (mailbox_id, uid)
);

-- Apart from messages, so listing a mailbox never reads a body.
CREATE TABLE bodies (
  message_id INTEGER PRIMARY KEY REFERENCES messages (id) ON DELETE CASCADE,
  raw        BLOB NOT NULL
);
`); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM users`)
		if err != nil {
			return err
		}
		var users []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			users = append(users, id)
		}
		rows.Close()
		for _, id := range users {
			if err := standardMailboxes(ctx, tx, id, time.Now()); err != nil {
				return err
			}
		}
		return nil
	},
}

// standard is frozen with this migration: the store keeps its own list, which may grow without
// changing what this did.
var standard = []struct{ name, specialUse string }{
	{"INBOX", ""},
	{"Sent", `\Sent`},
	{"Drafts", `\Drafts`},
	{"Archive", `\Archive`},
	{"Junk", `\Junk`},
	{"Trash", `\Trash`},
}

func standardMailboxes(ctx context.Context, tx *sql.Tx, userID string, now time.Time) error {
	for i, m := range standard {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mailboxes (user_id, name, special_use, uid_validity) VALUES (?, ?, ?, ?)`,
			userID, m.name, m.specialUse, uint32(now.Unix())+uint32(i)); err != nil {
			return err
		}
	}
	return nil
}
