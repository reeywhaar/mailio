package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"mailio/internal/store/migrations"
)

// A database whose default sent unknown mail to an account as junk keeps sending each following
// domain's there, now as the domain's own, and its default becomes reject.
func TestAJunkDefaultBecomesEachFollowingDomainsOwn(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// The database the migration before this one left, with a junk default: example.org and
	// example.net follow it, example.com drops its own.
	raw, err := sql.Open("sqlite", filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.RunBefore(ctx, raw, "20261110002144_junk_per_domain"); err != nil {
		t.Fatal(err)
	}
	const inbox, following, own = "ac_01inbox", "dm_01following", "dm_01own"
	for _, q := range []string{
		`INSERT INTO domains (id, name, created_at) VALUES ('dm_01org', 'example.org', 0)`,
		`INSERT INTO domains (id, name, created_at) VALUES ('` + following + `', 'example.net', 0)`,
		`INSERT INTO domains (id, name, unknown, unknown_own, created_at) VALUES ('` + own + `', 'example.com', 'drop', 1, 0)`,
		`INSERT INTO accounts (id, domain_id, local_part, created_at) VALUES ('` + inbox + `', 'dm_01org', 'inbox', 0)`,
		`UPDATE settings SET unknown = 'junk', unknown_account_id = '` + inbox + `'`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}
	raw.Close()

	st, err := Open(dir)
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
		{following, UnknownJunk, inbox},
		{own, UnknownDrop, ""},
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
