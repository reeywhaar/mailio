package migrations

// An account that sends as another: whatever it sends leaves as that account's, from its address,
// signed with its domain's key and through its relay, while its own mail still comes to it. Null
// sends as itself.
var sendAs = Migration{
	Name: "20261114005312_send_as",
	Up: exec(`
ALTER TABLE accounts ADD COLUMN send_as_account_id TEXT REFERENCES accounts (id);
`),
}
