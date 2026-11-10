package migrations

// How long Trash keeps a message before it is deleted for good: a default of 30 days, which a
// domain and then an account may set for themselves, null following the one above, and 0 keeping
// it forever. A message's time in Trash counts from when it was put there, which arrival does not
// say once it is moved, so every message records when it entered its mailbox; what is there
// already counts from now, and nothing goes the moment this ships.
var trashDays = Migration{
	Name: "20261110145826_trash_days",
	Up: exec(`
ALTER TABLE messages ADD COLUMN added_at INTEGER NOT NULL DEFAULT 0;
UPDATE messages SET added_at = CAST(strftime('%s', 'now') AS INTEGER);
ALTER TABLE settings ADD COLUMN trash_days INTEGER NOT NULL DEFAULT 30 CHECK (trash_days >= 0);
ALTER TABLE domains ADD COLUMN trash_days INTEGER CHECK (trash_days >= 0);
ALTER TABLE accounts ADD COLUMN trash_days INTEGER CHECK (trash_days >= 0);
`),
}
