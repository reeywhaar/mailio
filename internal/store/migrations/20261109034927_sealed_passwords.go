package migrations

// A user's password, sealed with MAILIO_SECRET_KEY beside its hash, so an admin can look it up
// again. Null for a password set before this, which only its hash knows.
var sealedPasswords = Migration{
	Name: "20261109034927_sealed_passwords",
	Up:   exec(`ALTER TABLE users ADD COLUMN password_sealed BLOB`),
}
