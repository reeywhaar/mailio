package migrations

// An alias is an address whose mail always goes to one account, which can send as it in turn. It
// is a row of accounts forwarding to that account, which signs in for it and holds its mail.
var aliases = Migration{
	Name: "20261110145012_aliases",
	Up: exec(`
ALTER TABLE accounts ADD COLUMN alias INTEGER NOT NULL DEFAULT 0;
`),
}
