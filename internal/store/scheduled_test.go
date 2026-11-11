package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"mailio/internal/store/migrations"
)

// Accounts from before Scheduled get one, and one with a mailbox of its own by that name has that
// one marked, mail and all.
func TestEveryAccountGetsScheduled(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	raw, err := sql.Open("sqlite", filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.RunBefore(ctx, raw, "20261111001827_scheduled"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO domains (id, name, created_at) VALUES ('dm_01org', 'example.org', 0)`,
		`INSERT INTO accounts (id, domain_id, local_part, created_at) VALUES ('ac_01inbox', 'dm_01org', 'inbox', 0)`,
		`INSERT INTO accounts (id, domain_id, local_part, created_at) VALUES ('ac_01own', 'dm_01org', 'own', 0)`,
		`INSERT INTO mailboxes (account_id, name, uid_validity) VALUES ('ac_01own', 'Scheduled', 1)`,
		`INSERT INTO accounts (id, domain_id, local_part, created_at, incoming, forward_account_id, alias)
		   VALUES ('ac_01alias', 'dm_01org', 'shop', 0, 'forward', 'ac_01inbox', 1)`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	raw.Close()

	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, id := range []string{"ac_01inbox", "ac_01own"} {
		if m, err := st.SpecialMailbox(ctx, id, Scheduled); err != nil || m.Name != "Scheduled" {
			t.Errorf("%s: %+v %v", id, m, err)
		}
	}
	if boxes, _ := st.Mailboxes(ctx, "ac_01alias"); len(boxes) != 0 {
		t.Errorf("the alias has %d mailboxes", len(boxes))
	}
}
