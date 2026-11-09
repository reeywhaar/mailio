package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"mailio/internal/address"
)

// The suite makes users by the dozen and is not testing bcrypt.
func TestMain(m *testing.M) {
	SetBcryptCost(bcrypt.MinCost)
	os.Exit(m.Run())
}

// A temporary file, not :memory:. WAL behaves differently in memory, and WAL is what is relied on.
func openStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

const pw = "correct horse"

// admin makes the first admin the way a fresh instance does: through a link.
func admin(t *testing.T, st *Store, addr string) *User {
	t.Helper()
	ctx := context.Background()
	token, _, err := st.CreateAdminLink(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.UseAdminLink(ctx, token, addr, pw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func resolve(t *testing.T, st *Store, addr string) *Recipient {
	t.Helper()
	a, err := address.Parse(addr)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.Resolve(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestEveryConnectionHasThePragmas(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	// Every connection a pool can open, held at once, so a pragma applied to only the first
	// would show.
	for _, pool := range []string{"writer", "reader"} {
		db := st.writer
		n := 1
		if pool == "reader" {
			db, n = st.reader, readers
		}
		for i := 0; i < n; i++ {
			c, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var fk, sync int
			if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&sync); err != nil {
				t.Fatal(err)
			}
			if fk != 1 || sync != 2 {
				t.Errorf("%s connection %d: foreign_keys=%d synchronous=%d", pool, i, fk, sync)
			}
		}
	}
}

func TestAMissingDataDirectoryRefusesToStart(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("opened a directory that does not exist")
	}
}

func TestReopeningAppliesNothingTwice(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	admin(t, st, "inbox@reey.ru")
	st.Close()

	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if has, _ := st.HasAdmin(context.Background()); !has {
		t.Error("the admin did not survive a reopen")
	}
}

func TestTheFirstAdminLinkMakesTheDomainAndTheUser(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if has, _ := st.HasAdmin(ctx); has {
		t.Fatal("a fresh database has an admin")
	}

	u := admin(t, st, "Inbox@Reey.ru")
	if u.Address() != "inbox@reey.ru" || !u.Admin {
		t.Errorf("got %s admin=%v", u.Address(), u.Admin)
	}
	if has, _ := st.HasAdmin(ctx); !has {
		t.Error("HasAdmin is false after the link was used")
	}
	d, err := st.DomainByName(ctx, "reey.ru")
	if err != nil {
		t.Fatal(err)
	}
	if d.Unknown != UnknownReject {
		t.Errorf("a new domain does %q with unknown mail", d.Unknown)
	}
	if _, err := st.Authenticate(ctx, "inbox@reey.ru", pw); err != nil {
		t.Errorf("cannot sign in with the password just set: %v", err)
	}
}

func TestAnAdminLinkWorksOnce(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	token, _, _ := st.CreateAdminLink(ctx)
	if _, err := st.UseAdminLink(ctx, token, "inbox@reey.ru", pw); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UseAdminLink(ctx, token, "other@reey.ru", pw); !errors.Is(err, ErrNotFound) {
		t.Errorf("second use: %v", err)
	}
}

func TestANewAdminLinkEndsTheOldOne(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	old, _, _ := st.CreateAdminLink(ctx)
	if _, _, err := st.CreateAdminLink(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdminLinkExpiry(ctx, old); !errors.Is(err, ErrNotFound) {
		t.Errorf("the old link is still live: %v", err)
	}
}

func TestAnAdminLinkExpires(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	token, _, _ := st.CreateAdminLink(ctx)
	later := time.Now().Add(AdminLinkLifetime + time.Minute)
	st.SetClock(func() time.Time { return later })
	if _, err := st.UseAdminLink(ctx, token, "inbox@reey.ru", pw); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired link: %v", err)
	}
}

func TestAMistypedAddressLeavesTheLinkUnspent(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	token, _, _ := st.CreateAdminLink(ctx)
	if _, err := st.UseAdminLink(ctx, token, "inbox+x@reey.ru", pw); !errors.Is(err, ErrInvalid) {
		t.Errorf("a tagged address: %v", err)
	}
	if _, err := st.UseAdminLink(ctx, token, "inbox@reey.ru", "short"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a short password: %v", err)
	}
	if _, err := st.UseAdminLink(ctx, token, "inbox@reey.ru", pw); err != nil {
		t.Errorf("the link was spent by a refusal: %v", err)
	}
}

func TestAnAdminLinkOnAnExistingUserResetsItsPasswordAndSignsItOut(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "inbox@reey.ru")
	u, err := st.CreateUser(ctx, "misha@reey.ru", "old password", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, "cookie", u.ID, ""); err != nil {
		t.Fatal(err)
	}

	token, _, _ := st.CreateAdminLink(ctx)
	got, err := st.UseAdminLink(ctx, token, "misha@reey.ru", "new password")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID || !got.Admin {
		t.Errorf("got %s admin=%v, want the existing user made admin", got.ID, got.Admin)
	}
	if _, err := st.Authenticate(ctx, "misha@reey.ru", "old password"); err == nil {
		t.Error("the old password still works")
	}
	if _, err := st.Authenticate(ctx, "misha@reey.ru", "new password"); err != nil {
		t.Errorf("the new password does not: %v", err)
	}
	if _, err := st.SessionByToken(ctx, "cookie"); !errors.Is(err, ErrNotFound) {
		t.Error("the old session survived")
	}
}

func TestAUserNeedsADomainMailioReceivesFor(t *testing.T) {
	st := openStore(t)
	if _, err := st.CreateUser(context.Background(), "inbox@vyrtsev.com", pw, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("got %v", err)
	}
}

func TestAddressesAreUniqueRegardlessOfCase(t *testing.T) {
	st := openStore(t)
	admin(t, st, "inbox@reey.ru")
	if _, err := st.CreateUser(context.Background(), "INBOX@reey.ru", pw, false); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v", err)
	}
}

func TestAWrongPasswordAndAMissingUserAreOneRefusal(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "inbox@reey.ru")
	for _, c := range [][2]string{{"inbox@reey.ru", "wrong password"}, {"nobody@reey.ru", pw}, {"not an address", pw}} {
		if _, err := st.Authenticate(ctx, c[0], c[1]); err != ErrNotFound {
			t.Errorf("%q: got %v, want the bare ErrNotFound", c[0], err)
		}
	}
}

func TestTheLastAdminStaysAnAdmin(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	u := admin(t, st, "inbox@reey.ru")
	if err := st.SetAdmin(ctx, u.ID, false); !errors.Is(err, ErrConflict) {
		t.Errorf("demoting the last admin: %v", err)
	}
	if err := st.DeleteUser(ctx, u.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("deleting the last admin: %v", err)
	}

	other, _ := st.CreateUser(ctx, "misha@reey.ru", pw, true)
	if err := st.SetAdmin(ctx, u.ID, false); err != nil {
		t.Errorf("demoting with another admin present: %v", err)
	}
	if err := st.DeleteUser(ctx, other.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("deleting the admin that is now the last: %v", err)
	}
}

func TestLosingAdminSignsOut(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "inbox@reey.ru")
	u, _ := st.CreateUser(ctx, "misha@reey.ru", pw, true)
	st.CreateSession(ctx, "cookie", u.ID, "")
	if err := st.SetAdmin(ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionByToken(ctx, "cookie"); !errors.Is(err, ErrNotFound) {
		t.Error("the session outlived the admin flag")
	}
}

func TestMailToATaggedAddressGoesToItsUser(t *testing.T) {
	st := openStore(t)
	u := admin(t, st, "inbox@reey.ru")
	r := resolve(t, st, "Inbox+Shop@reey.ru")
	if !r.Ours || r.User == nil || r.User.ID != u.ID || r.Tag != "shop" || r.Junk || r.Drop {
		t.Errorf("got %+v", r)
	}
}

func TestMailToAnotherDomainIsNotOurs(t *testing.T) {
	st := openStore(t)
	admin(t, st, "inbox@reey.ru")
	if r := resolve(t, st, "inbox@gmail.com"); r.Ours {
		t.Errorf("got %+v", r)
	}
}

func TestADomainDecidesWhatHappensToUnknownMail(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	u := admin(t, st, "inbox@reey.ru")
	d, _ := st.DomainByName(ctx, "reey.ru")

	if r := resolve(t, st, "nobody@reey.ru"); !r.Ours || r.User != nil || r.Drop || r.Junk {
		t.Errorf("reject: got %+v", r)
	}

	if err := st.SetUnknown(ctx, d.ID, UnknownDrop, ""); err != nil {
		t.Fatal(err)
	}
	if r := resolve(t, st, "nobody@reey.ru"); r.User != nil || !r.Drop {
		t.Errorf("drop: got %+v", r)
	}

	if err := st.SetUnknown(ctx, d.ID, UnknownJunk, u.ID); err != nil {
		t.Fatal(err)
	}
	if r := resolve(t, st, "nobody+x@reey.ru"); r.User == nil || r.User.ID != u.ID || !r.Junk {
		t.Errorf("junk: got %+v", r)
	}
	if r := resolve(t, st, "inbox@reey.ru"); r.Junk {
		t.Error("mail to a real address was marked junk")
	}
}

func TestJunkNeedsSomebodyToGoTo(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	u := admin(t, st, "inbox@reey.ru")
	d, _ := st.DomainByName(ctx, "reey.ru")
	if err := st.SetUnknown(ctx, d.ID, UnknownJunk, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("junk to nobody: %v", err)
	}
	if err := st.SetUnknown(ctx, d.ID, UnknownDrop, u.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("drop to somebody: %v", err)
	}
	if err := st.SetUnknown(ctx, d.ID, "bounce", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("a made-up setting: %v", err)
	}
}

func TestTheUserGettingJunkCannotBeDeletedFromUnderIt(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "inbox@reey.ru")
	d, _ := st.DomainByName(ctx, "reey.ru")
	u, _ := st.CreateUser(ctx, "junk@reey.ru", pw, false)
	st.SetUnknown(ctx, d.ID, UnknownJunk, u.ID)
	if err := st.DeleteUser(ctx, u.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v", err)
	}
}

func TestADomainWithUsersCannotBeDeleted(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "inbox@reey.ru")
	d, _ := st.DomainByName(ctx, "reey.ru")
	if err := st.DeleteDomain(ctx, d.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v", err)
	}

	empty, err := st.CreateDomain(ctx, "vyrtsev.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteDomain(ctx, empty.ID); err != nil {
		t.Errorf("deleting an empty domain: %v", err)
	}
}
