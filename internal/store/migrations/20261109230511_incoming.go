package migrations

// What a user does with mail sent to it: keeps it, refuses it (a no-reply address that only
// sends), or forwards it into another user's mailbox.
var incoming = Migration{
	Name: "20261109230511_incoming",
	Up: exec(`
ALTER TABLE users ADD COLUMN incoming TEXT NOT NULL DEFAULT 'keep' CHECK (incoming IN ('keep', 'reject', 'forward'));
-- Set exactly when incoming is 'forward'; the store holds the two together.
ALTER TABLE users ADD COLUMN forward_user_id TEXT REFERENCES users (id);
`),
}
