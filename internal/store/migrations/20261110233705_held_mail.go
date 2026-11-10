package migrations

// A message can be held until a time its sender chose (FUTURERELEASE, RFC 4865): it waits in the
// queue until release_at, and is retried and given up on counting from then. Null is sent at once.
var heldMail = Migration{
	Name: "20261110233705_held_mail",
	Up: exec(`
ALTER TABLE outbound_messages ADD COLUMN release_at INTEGER;
`),
}
