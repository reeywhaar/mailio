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

// admin makes an admin the way a fresh instance does: through a link.
func admin(t *testing.T, st *Store, name string) *Admin {
	t.Helper()
	ctx := context.Background()
	token, _, err := st.CreateAdminLink(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.UseAdminLink(ctx, token, name, pw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// user makes a user, and its domain first if mailio does not receive for it yet.
func user(t *testing.T, st *Store, addr string) *User {
	t.Helper()
	ctx := context.Background()
	a, err := address.ParseUser(addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DomainByName(ctx, a.Domain); errors.Is(err, ErrNotFound) {
		if _, err := st.CreateDomain(ctx, a.Domain); err != nil {
			t.Fatal(err)
		}
	}
	u, err := st.CreateUser(ctx, addr, pw)
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
	admin(t, st, "misha")
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

func TestTheFirstAdminLinkMakesAnAdminAndNothingElse(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	if has, _ := st.HasAdmin(ctx); has {
		t.Fatal("a fresh database has an admin")
	}

	a := admin(t, st, "Misha")
	if a.Name != "misha" {
		t.Errorf("got %q", a.Name)
	}
	if has, _ := st.HasAdmin(ctx); !has {
		t.Error("HasAdmin is false after the link was used")
	}
	if _, err := st.AuthenticateAdmin(ctx, "misha", pw); err != nil {
		t.Errorf("cannot sign in with the password just set: %v", err)
	}
	// An admin is not a mailbox: no domain, no user.
	if domains, _ := st.Domains(ctx); len(domains) != 0 {
		t.Errorf("the link added domains: %v", domains)
	}
	if users, _ := st.Users(ctx); len(users) != 0 {
		t.Errorf("the link added users: %v", users)
	}
}

func TestAnAdminLinkWorksOnce(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	token, _, _ := st.CreateAdminLink(ctx)
	if _, err := st.UseAdminLink(ctx, token, "misha", pw); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UseAdminLink(ctx, token, "other", pw); !errors.Is(err, ErrNotFound) {
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
	if _, err := st.UseAdminLink(ctx, token, "misha", pw); !errors.Is(err, ErrNotFound) {
		t.Errorf("an expired link: %v", err)
	}
}

func TestARefusedNameLeavesTheLinkUnspent(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	token, _, _ := st.CreateAdminLink(ctx)
	if _, err := st.UseAdminLink(ctx, token, "inbox@reey.ru", pw); !errors.Is(err, ErrInvalid) {
		t.Errorf("an address as a name: %v", err)
	}
	if _, err := st.UseAdminLink(ctx, token, "misha", "short"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a short password: %v", err)
	}
	if _, err := st.UseAdminLink(ctx, token, "misha", pw); err != nil {
		t.Errorf("the link was spent by a refusal: %v", err)
	}
}

func TestAnAdminLinkOnAnExistingAdminResetsTheirPasswordAndSignsThemOut(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	a := admin(t, st, "misha")
	if _, err := st.CreateSession(ctx, "cookie", a.ID, ""); err != nil {
		t.Fatal(err)
	}

	token, _, _ := st.CreateAdminLink(ctx)
	got, err := st.UseAdminLink(ctx, token, "misha", "new password")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID {
		t.Errorf("got %s, want the existing admin", got.ID)
	}
	if _, err := st.AuthenticateAdmin(ctx, "misha", pw); err == nil {
		t.Error("the old password still works")
	}
	if _, err := st.AuthenticateAdmin(ctx, "misha", "new password"); err != nil {
		t.Errorf("the new password does not: %v", err)
	}
	if _, err := st.SessionByToken(ctx, "cookie"); !errors.Is(err, ErrNotFound) {
		t.Error("the old session survived")
	}
}

func TestAWrongPasswordAndAMissingAdminAreOneRefusal(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "misha")
	for _, c := range [][2]string{{"misha", "wrong password"}, {"nobody", pw}, {"not a name!", pw}} {
		if _, err := st.AuthenticateAdmin(ctx, c[0], c[1]); err != ErrNotFound {
			t.Errorf("%q: got %v, want the bare ErrNotFound", c[0], err)
		}
	}
}

func TestAUserIsNotAnAdmin(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "misha")
	user(t, st, "inbox@reey.ru")
	if _, err := st.AuthenticateAdmin(ctx, "inbox@reey.ru", pw); err != ErrNotFound {
		t.Errorf("a user signed in as an admin: %v", err)
	}
}

func TestAdminNamesAreUniqueRegardlessOfCase(t *testing.T) {
	st := openStore(t)
	admin(t, st, "misha")
	if _, err := st.CreateAdmin(context.Background(), "MISHA", pw); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v", err)
	}
}

func TestTheLastAdminStays(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	a := admin(t, st, "misha")
	if err := st.DeleteAdmin(ctx, a.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("deleting the last admin: %v", err)
	}
	other, err := st.CreateAdmin(ctx, "anna", pw)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAdmin(ctx, a.ID); err != nil {
		t.Errorf("deleting with another admin present: %v", err)
	}
	if err := st.DeleteAdmin(ctx, other.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("deleting the admin that is now the last: %v", err)
	}
}

func TestANewAdminPasswordSignsOutEverywhereButHere(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	a := admin(t, st, "misha")
	st.CreateSession(ctx, "here", a.ID, "")
	st.CreateSession(ctx, "there", a.ID, "")
	if err := st.SetAdminPassword(ctx, a.ID, "new password", "here"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionByToken(ctx, "here"); err != nil {
		t.Errorf("the session making the change ended: %v", err)
	}
	if _, err := st.SessionByToken(ctx, "there"); !errors.Is(err, ErrNotFound) {
		t.Error("another session outlived the password")
	}
}

func TestADeletedAdminIsSignedOut(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "misha")
	a, _ := st.CreateAdmin(ctx, "anna", pw)
	st.CreateSession(ctx, "cookie", a.ID, "")
	if err := st.DeleteAdmin(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionByToken(ctx, "cookie"); !errors.Is(err, ErrNotFound) {
		t.Error("the session outlived the admin")
	}
}

func TestAUserNeedsADomainMailioReceivesFor(t *testing.T) {
	st := openStore(t)
	if _, err := st.CreateUser(context.Background(), "inbox@vyrtsev.com", pw); !errors.Is(err, ErrInvalid) {
		t.Errorf("got %v", err)
	}
}

func TestAddressesAreUniqueRegardlessOfCase(t *testing.T) {
	st := openStore(t)
	user(t, st, "inbox@reey.ru")
	if _, err := st.CreateUser(context.Background(), "INBOX@reey.ru", pw); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v", err)
	}
}

func TestAWrongPasswordAndAMissingUserAreOneRefusal(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	user(t, st, "inbox@reey.ru")
	for _, c := range [][2]string{{"inbox@reey.ru", "wrong password"}, {"nobody@reey.ru", pw}, {"not an address", pw}} {
		if _, err := st.Authenticate(ctx, c[0], c[1]); err != ErrNotFound {
			t.Errorf("%q: got %v, want the bare ErrNotFound", c[0], err)
		}
	}
}

func TestMailToATaggedAddressGoesToItsUser(t *testing.T) {
	st := openStore(t)
	u := user(t, st, "inbox@reey.ru")
	r := resolve(t, st, "Inbox+Shop@reey.ru")
	if !r.Ours || r.User == nil || r.User.ID != u.ID || r.Tag != "shop" || r.Junk || r.Drop {
		t.Errorf("got %+v", r)
	}
}

func TestMailToAnotherDomainIsNotOurs(t *testing.T) {
	st := openStore(t)
	user(t, st, "inbox@reey.ru")
	if r := resolve(t, st, "inbox@gmail.com"); r.Ours {
		t.Errorf("got %+v", r)
	}
}

func TestADomainDecidesWhatHappensToUnknownMail(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	u := user(t, st, "inbox@reey.ru")
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
	u := user(t, st, "inbox@reey.ru")
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
	user(t, st, "inbox@reey.ru")
	d, _ := st.DomainByName(ctx, "reey.ru")
	u, _ := st.CreateUser(ctx, "junk@reey.ru", pw)
	st.SetUnknown(ctx, d.ID, UnknownJunk, u.ID)
	if err := st.DeleteUser(ctx, u.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v", err)
	}
}

func TestADomainWithUsersCannotBeDeleted(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	user(t, st, "inbox@reey.ru")
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
