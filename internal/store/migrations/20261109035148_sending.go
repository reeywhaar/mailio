package migrations

// Sending: each domain's DKIM key, the relay mail is handed to when the host may not send on port
// 25 itself, and the queue of messages on their way out.
var sending = Migration{
	Name: "20261109035148_sending",
	Up: exec(`
-- Empty until mailio makes the key, which it does for every domain at startup.
ALTER TABLE domains ADD COLUMN dkim_selector TEXT NOT NULL DEFAULT '';
ALTER TABLE domains ADD COLUMN dkim_key TEXT NOT NULL DEFAULT '';

-- One row or none. None: mailio delivers to each recipient's MX itself.
CREATE TABLE relay (
  id              INTEGER PRIMARY KEY CHECK (id = 1),
  host            TEXT NOT NULL,
  port            INTEGER NOT NULL,
  security        TEXT NOT NULL CHECK (security IN ('starttls', 'implicit')),
  username        TEXT NOT NULL DEFAULT '',
  -- Sealed with MAILIO_SECRET_KEY, like users' passwords.
  password_sealed BLOB
);

-- A message being sent, once however many recipients it has.
CREATE TABLE outbound_messages (
  id         INTEGER PRIMARY KEY,
  -- The envelope sender, and the user it came from, who hears about a failure.
  sender     TEXT NOT NULL,
  user_id    TEXT REFERENCES users (id) ON DELETE SET NULL,
  raw        BLOB NOT NULL,
  created_at INTEGER NOT NULL
);

-- One recipient still to deliver to. Delivered or given up on, the row goes.
CREATE TABLE outbound_recipients (
  id         INTEGER PRIMARY KEY,
  message_id INTEGER NOT NULL REFERENCES outbound_messages (id) ON DELETE CASCADE,
  recipient  TEXT NOT NULL,
  attempts   INTEGER NOT NULL DEFAULT 0,
  next_at    INTEGER NOT NULL,
  last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX outbound_due ON outbound_recipients (next_at);
`),
}
