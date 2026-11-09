package migrations

// The mail ports' certificate, and the Let's Encrypt account that renews it. Kept with everything
// else, so a backup restores TLS along with the mail.
var certificates = Migration{
	Name: "20261109034425_certificates",
	Up: exec(`
CREATE TABLE certificates (
  host       TEXT PRIMARY KEY,
  -- PEM: the chain, then the key.
  chain      TEXT NOT NULL,
  key        TEXT NOT NULL,
  -- "self" or "letsencrypt".
  issuer     TEXT NOT NULL,
  not_after  INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

-- One row: the ACME account key. Losing it costs nothing but a new registration.
CREATE TABLE acme_account (
  id  INTEGER PRIMARY KEY CHECK (id = 1),
  key TEXT NOT NULL
);
`),
}
