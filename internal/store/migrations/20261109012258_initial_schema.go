package migrations

// Domains, users, and how an admin gets in. Mail arrives in migrations of its own.
var initialSchema = Migration{
	Name: "20261109012258_initial_schema",
	Up: exec(`
CREATE TABLE domains (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL UNIQUE,
  -- What happens to mail for an address here that no user has. See docs/addresses.md.
  unknown         TEXT NOT NULL DEFAULT 'reject' CHECK (unknown IN ('reject', 'drop', 'junk')),
  unknown_user_id TEXT REFERENCES users (id),
  created_at      INTEGER NOT NULL,
  CHECK ((unknown = 'junk') = (unknown_user_id IS NOT NULL))
);

CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  domain_id     TEXT NOT NULL REFERENCES domains (id),
  -- Lowercase, and never with a + in it: that is where a tag begins.
  local_part    TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  admin         INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  UNIQUE (domain_id, local_part)
);

-- One live link at a time, so the table is its token hash and nothing else.
CREATE TABLE admin_links (
  token_hash BLOB PRIMARY KEY,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE sessions (
  id_hash      BLOB PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  user_agent   TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expiry ON sessions (expires_at);
`),
}
