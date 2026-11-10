package migrations

// How an account's mail goes out, as a domain's does: null follows its domain's, 'builtin'
// delivers directly, and 'relay' goes through relay_id.
var accountRelay = Migration{
	Name: "20261110144531_account_relay",
	Up: exec(`
ALTER TABLE accounts ADD COLUMN send_via TEXT CHECK (send_via IN ('builtin', 'relay'));
ALTER TABLE accounts ADD COLUMN relay_id TEXT REFERENCES relays (id);
`),
}
