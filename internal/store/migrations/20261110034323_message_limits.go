package migrations

// The largest message is a default, 50 MB as it was fixed before, which a domain and then an
// account may set for themselves: null follows the one above.
var messageLimits = Migration{
	Name: "20261110034323_message_limits",
	Up: exec(`
ALTER TABLE settings ADD COLUMN max_message_bytes INTEGER NOT NULL DEFAULT 52428800 CHECK (max_message_bytes > 0);
ALTER TABLE domains ADD COLUMN max_message_bytes INTEGER CHECK (max_message_bytes > 0);
ALTER TABLE accounts ADD COLUMN max_message_bytes INTEGER CHECK (max_message_bytes > 0);
`),
}
