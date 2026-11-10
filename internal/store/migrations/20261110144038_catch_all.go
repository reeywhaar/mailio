package migrations

// A domain's catch-all can forward too: mail for an address nobody has goes to the chosen account's
// INBOX, where junk goes to its Junk folder. The CHECK that names the choices is part of the table,
// so the table is made again, with every row and column as it was.
var catchAll = Migration{
	Name:    "20261110144038_catch_all",
	Rebuild: true,
	Up: exec(`
CREATE TABLE domains_new (
  id                 TEXT PRIMARY KEY,
  name               TEXT NOT NULL UNIQUE,
  -- The catch-all: what happens to mail for an address here that no account has. See
  -- docs/addresses.md.
  unknown            TEXT NOT NULL DEFAULT 'reject' CHECK (unknown IN ('reject', 'drop', 'forward', 'junk')),
  -- Who gets it, for forward and junk.
  unknown_account_id TEXT REFERENCES accounts (id),
  -- Whether the two above are the domain's own, or it follows the default.
  unknown_own        INTEGER NOT NULL DEFAULT 0,
  created_at         INTEGER NOT NULL,
  dkim_selector      TEXT NOT NULL DEFAULT '',
  dkim_key           TEXT NOT NULL DEFAULT '',
  -- The domain's own; null follows the default. send_via 'builtin' delivers directly, 'relay'
  -- goes through relay_id.
  dmarc              TEXT CHECK (dmarc IN ('none', 'quarantine', 'reject')),
  spf                TEXT CHECK (spf IN ('-all', '~all', '?all')),
  send_via           TEXT CHECK (send_via IN ('builtin', 'relay')),
  relay_id           TEXT REFERENCES relays (id),
  max_message_bytes  INTEGER CHECK (max_message_bytes > 0),
  CHECK ((unknown IN ('forward', 'junk')) = (unknown_account_id IS NOT NULL))
);
INSERT INTO domains_new (id, name, unknown, unknown_account_id, unknown_own, created_at, dkim_selector,
                         dkim_key, dmarc, spf, send_via, relay_id, max_message_bytes)
SELECT id, name, unknown, unknown_account_id, unknown_own, created_at, dkim_selector,
       dkim_key, dmarc, spf, send_via, relay_id, max_message_bytes
  FROM domains;
DROP TABLE domains;
ALTER TABLE domains_new RENAME TO domains;
`),
}
