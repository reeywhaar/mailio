package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// A database whose default sent unknown mail to an account as junk keeps sending each following
// domain's there, now as the domain's own, and its default becomes reject.
func TestAJunkDefaultBecomesEachFollowingDomainsOwn(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	inbox := account(t, st, "inbox@reey.ru")
	following, err := st.CreateDomain(ctx, "vyrtsev.com")
	if err != nil {
		t.Fatal(err)
	}
	own, err := st.CreateDomain(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUnknown(ctx, own.ID, UnknownDrop, ""); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// The settings as the migration before this one left them, with a junk default, and the
	// migrations since undone.
	raw, err := sql.Open("sqlite", filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := raw.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TABLE settings`,
		`CREATE TABLE settings (
		   id INTEGER PRIMARY KEY CHECK (id = 1),
		   unknown TEXT NOT NULL DEFAULT 'reject' CHECK (unknown IN ('reject', 'drop', 'junk')),
		   unknown_account_id TEXT REFERENCES accounts (id),
		   dmarc TEXT NOT NULL DEFAULT 'reject',
		   spf TEXT NOT NULL DEFAULT '~all',
		   relay_id TEXT REFERENCES relays (id),
		   CHECK ((unknown = 'junk') = (unknown_account_id IS NOT NULL)))`,
		`INSERT INTO settings (id, unknown, unknown_account_id) VALUES (1, 'junk', '` + inbox.ID + `')`,
		`ALTER TABLE domains DROP COLUMN max_message_bytes`,
		`ALTER TABLE accounts DROP COLUMN max_message_bytes`,
		fmt.Sprintf(`PRAGMA user_version = %d`, version-2),
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	raw.Close()

	st, err = Open(dir)
	if err != nil {
		t.Fatalf("the database does not open: %v", err)
	}
	defer st.Close()
	set, err := st.Settings(ctx)
	if err != nil || set.Unknown != UnknownReject {
		t.Errorf("the default: %+v %v", set, err)
	}
	for _, c := range []struct {
		id      string
		unknown Unknown
		to      string
	}{
		{following.ID, UnknownJunk, inbox.ID},
		{own.ID, UnknownDrop, ""},
	} {
		d, err := st.DomainByID(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if u, to := d.UnknownFor(set); !d.UnknownOwn || u != c.unknown || to != c.to {
			t.Errorf("%s: own %v, %s to %q", d.Name, d.UnknownOwn, u, to)
		}
	}
}
