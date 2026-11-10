package migrations

// How long Junk keeps a message before it is deleted for good, as Trash has: a default of 30
// days, which a domain and then an account may set for themselves, null following the one above,
// and 0 keeping it forever. Counted from when the message was put there, which messages record.
var junkDays = Migration{
	Name: "20261110182542_junk_days",
	Up: exec(`
ALTER TABLE settings ADD COLUMN junk_days INTEGER NOT NULL DEFAULT 30 CHECK (junk_days >= 0);
ALTER TABLE domains ADD COLUMN junk_days INTEGER CHECK (junk_days >= 0);
ALTER TABLE accounts ADD COLUMN junk_days INTEGER CHECK (junk_days >= 0);
`),
}
