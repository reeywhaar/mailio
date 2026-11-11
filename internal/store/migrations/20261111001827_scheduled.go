package migrations

// Mail held to send later is shown to its sender in a Scheduled mailbox (RFC 9979), a copy of each
// held message linked to it from the queue: deleting the copy cancels it, and when it goes it is
// filed in Sent. Every account gets the mailbox; one with its own called Scheduled has that one
// marked.
var scheduled = Migration{
	Name: "20261111001827_scheduled",
	Up: exec(`
ALTER TABLE outbound_messages ADD COLUMN scheduled_id INTEGER REFERENCES messages (id) ON DELETE SET NULL;

UPDATE mailboxes SET special_use = '\Scheduled' WHERE name = 'Scheduled' AND special_use = '';
INSERT INTO mailboxes (account_id, name, special_use, uid_validity)
SELECT id, 'Scheduled', '\Scheduled', CAST(strftime('%s', 'now') AS INTEGER)
  FROM accounts
 WHERE NOT alias AND id NOT IN (SELECT account_id FROM mailboxes WHERE name = 'Scheduled');
`),
}
