package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// firstBuildSchema is the initial migration as the first v2 build ran it, before admins had a
// table of their own: what a database deployed from that build still holds.
const firstBuildSchema = `
CREATE TABLE domains (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL UNIQUE,
  -- What happens to mail for an address here that no user has. See docs/addresses.md.
  unknown         TEXT NOT NULL DEFAULT 'reject' CHECK (unknown IN ('reject', 'drop', 'junk')),
  unknown_user_id TEXT REFERENCES users (id),
  created_at      INTEGER NOT NULL,
  CHECK ((unknown = 'junk') = (unknown_user_id IS NOT NULL))
);

CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  domain_id     TEXT NOT NULL REFERENCES domains (id),
  -- Lowercase, and never with a + in it: that is where a tag begins.
  local_part    TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  admin         INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  UNIQUE (domain_id, local_part)
);

-- One live link at a time, so the table is its token hash and nothing else.
CREATE TABLE admin_links (
  token_hash BLOB PRIMARY KEY,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE sessions (
  id_hash      BLOB PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  user_agent   TEXT NOT NULL DEFAULT '',
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expiry ON sessions (expires_at);
`

// A database the first build made opens with this one: admins get their table, sessions are
// keyed by admin, and its mail accounts are untouched.
func TestADatabaseFromTheFirstBuildIsRepaired(t *testing.T) {
	dir := t.TempDir()
	raw, err := sql.Open("sqlite", filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	for _, q := range []string{
		firstBuildSchema,
		`PRAGMA user_version = 1`,
		`INSERT INTO domains (id, name, created_at) VALUES ('dm_01m6z3hmy1v8qbhpactxqd87qk', 'example.org', 1)`,
		`INSERT INTO users (id, domain_id, local_part, password_hash, admin, created_at)
		   VALUES ('u_01m6z3hney6z230fhvepsbfacv', 'dm_01m6z3hmy1v8qbhpactxqd87qk', 'inbox', '` + string(hash) + `', 1, 1)`,
		`INSERT INTO sessions (id_hash, user_id, created_at, last_seen_at, expires_at)
		   VALUES (x'00', 'u_01m6z3hney6z230fhvepsbfacv', 1, 1, 99999999999)`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	raw.Close()

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("the first build's database does not open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// Its flagged account is an account, not an admin, so serve will log an admin link.
	if has, err := st.HasAdmin(ctx); err != nil || has {
		t.Errorf("has admin: %v %v", has, err)
	}
	a := admin(t, st, "misha")
	if _, err := st.CreateSession(ctx, "cookie", a.ID, ""); err != nil {
		t.Errorf("a session: %v", err)
	}
	u, err := st.Authenticate(ctx, "inbox@example.org", pw)
	if err != nil {
		t.Fatalf("the mail account: %v", err)
	}
	if !strings.HasPrefix(u.ID, "ac_") {
		t.Errorf("the account kept its old id %s", u.ID)
	}
	if _, err := st.MailboxNamed(ctx, u.ID, Inbox); err != nil {
		t.Errorf("the mail user has no INBOX: %v", err)
	}
	if _, err := st.CreateAccount(ctx, "other@example.org", pw); err != nil {
		t.Errorf("a new user: %v", err)
	}
	if err := st.DeleteAccount(ctx, u.ID); err != nil && !errors.Is(err, ErrConflict) {
		t.Errorf("deleting the old user: %v", err)
	}
}
