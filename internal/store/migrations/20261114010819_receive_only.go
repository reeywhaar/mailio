package migrations

// An account that does not send: its logins read its mail, and what they try to send is refused.
var receiveOnly = Migration{
	Name: "20261114010819_receive_only",
	Up: exec(`
ALTER TABLE accounts ADD COLUMN sends INTEGER NOT NULL DEFAULT 1;
`),
}
