package migrations

// What received mail and signed in to read it was a user, and its logins were passwords; they are
// an account and its credentials. Ids take the new prefixes, and every reference to one with them.
var accounts = Migration{
	Name: "20261109235411_accounts",
	Up: exec(`
-- Checked at commit, once every reference has its new id.
PRAGMA defer_foreign_keys = ON;

ALTER TABLE users RENAME TO accounts;
ALTER TABLE accounts RENAME COLUMN forward_user_id TO forward_account_id;
ALTER TABLE user_passwords RENAME TO credentials;
ALTER TABLE credentials RENAME COLUMN user_id TO account_id;
ALTER TABLE mailboxes RENAME COLUMN user_id TO account_id;
ALTER TABLE domains RENAME COLUMN unknown_user_id TO unknown_account_id;
ALTER TABLE settings RENAME COLUMN unknown_user_id TO unknown_account_id;
ALTER TABLE outbound_messages RENAME COLUMN user_id TO account_id;

UPDATE accounts SET id = 'ac_' || substr(id, 3), forward_account_id = 'ac_' || substr(forward_account_id, 3);
UPDATE credentials SET id = 'cr_' || substr(id, 4), account_id = 'ac_' || substr(account_id, 3);
UPDATE mailboxes SET account_id = 'ac_' || substr(account_id, 3);
UPDATE domains SET unknown_account_id = 'ac_' || substr(unknown_account_id, 3);
UPDATE settings SET unknown_account_id = 'ac_' || substr(unknown_account_id, 3);
UPDATE outbound_messages SET account_id = 'ac_' || substr(account_id, 3);
`),
}
