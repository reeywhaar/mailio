package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"mailio/internal/address"
	"mailio/internal/seal"
)

// The suite makes accounts by the dozen and is not testing bcrypt.
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

// account makes an account, and its domain first if mailio does not receive for it yet.
func account(t *testing.T, st *Store, addr string) *Account {
	t.Helper()
	ctx := context.Background()
	a, err := address.ParseAccount(addr)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DomainByName(ctx, a.Domain); errors.Is(err, ErrNotFound) {
		if _, err := st.CreateDomain(ctx, a.Domain); err != nil {
			t.Fatal(err)
		}
	}
	u, err := st.CreateAccount(ctx, addr, pw)
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
	// An admin is not a mailbox: no domain, no account.
	if domains, _ := st.Domains(ctx); len(domains) != 0 {
		t.Errorf("the link added domains: %v", domains)
	}
	if accounts, _ := st.Accounts(ctx); len(accounts) != 0 {
		t.Errorf("the link added accounts: %v", accounts)
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

func TestAnAccountIsNotAnAdmin(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	admin(t, st, "misha")
	account(t, st, "inbox@reey.ru")
	if _, err := st.AuthenticateAdmin(ctx, "inbox@reey.ru", pw); err != ErrNotFound {
		t.Errorf("an account signed in as an admin: %v", err)
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

func TestAnAccountNeedsADomainMailioReceivesFor(t *testing.T) {
	st := openStore(t)
	if _, err := st.CreateAccount(context.Background(), "inbox@vyrtsev.com", pw); !errors.Is(err, ErrInvalid) {
		t.Errorf("got %v", err)
	}
}

func TestAddressesAreUniqueRegardlessOfCase(t *testing.T) {
	st := openStore(t)
	account(t, st, "inbox@reey.ru")
	if _, err := st.CreateAccount(context.Background(), "INBOX@reey.ru", pw); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v", err)
	}
}

func TestAWrongPasswordAndAMissingAccountAreOneRefusal(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	account(t, st, "inbox@reey.ru")
	for _, c := range [][2]string{{"inbox@reey.ru", "wrong password"}, {"nobody@reey.ru", pw}, {"not an address", pw}} {
		if _, err := st.Authenticate(ctx, c[0], c[1]); err != ErrNotFound {
			t.Errorf("%q: got %v, want the bare ErrNotFound", c[0], err)
		}
	}
}

func TestMailToATaggedAddressGoesToItsAccount(t *testing.T) {
	st := openStore(t)
	u := account(t, st, "inbox@reey.ru")
	r := resolve(t, st, "Inbox+Shop@reey.ru")
	if !r.Ours || r.Account == nil || r.Account.ID != u.ID || r.Tag != "shop" || r.Junk || r.Drop {
		t.Errorf("got %+v", r)
	}
}

func TestMailToAnotherDomainIsNotOurs(t *testing.T) {
	st := openStore(t)
	account(t, st, "inbox@reey.ru")
	if r := resolve(t, st, "inbox@gmail.com"); r.Ours {
		t.Errorf("got %+v", r)
	}
}

func TestADomainDecidesWhatHappensToUnknownMail(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	u := account(t, st, "inbox@reey.ru")
	d, _ := st.DomainByName(ctx, "reey.ru")

	if r := resolve(t, st, "nobody@reey.ru"); !r.Ours || r.Account != nil || r.Drop || r.Junk {
		t.Errorf("reject: got %+v", r)
	}

	if err := st.SetUnknown(ctx, d.ID, UnknownDrop, ""); err != nil {
		t.Fatal(err)
	}
	if r := resolve(t, st, "nobody@reey.ru"); r.Account != nil || !r.Drop {
		t.Errorf("drop: got %+v", r)
	}

	if err := st.SetUnknown(ctx, d.ID, UnknownJunk, u.ID); err != nil {
		t.Fatal(err)
	}
	if r := resolve(t, st, "nobody+x@reey.ru"); r.Account == nil || r.Account.ID != u.ID || !r.Junk {
		t.Errorf("junk: got %+v", r)
	}
	if r := resolve(t, st, "inbox@reey.ru"); r.Junk {
		t.Error("mail to a real address was marked junk")
	}
}

func TestJunkNeedsSomebodyToGoTo(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	u := account(t, st, "inbox@reey.ru")
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

func TestTheAccountGettingJunkCannotBeDeletedFromUnderIt(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	account(t, st, "inbox@reey.ru")
	d, _ := st.DomainByName(ctx, "reey.ru")
	u, _ := st.CreateAccount(ctx, "junk@reey.ru", pw)
	st.SetUnknown(ctx, d.ID, UnknownJunk, u.ID)
	if err := st.DeleteAccount(ctx, u.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("got %v", err)
	}
}

func TestADomainWithAccountsCannotBeDeleted(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	account(t, st, "inbox@reey.ru")
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

func TestEachPasswordHasItsOwnLoginAndOnlyThatOne(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	u := account(t, st, "inbox@reey.ru")

	phone, err := st.AddCredential(ctx, u.ID, "iPhone", "phone password")
	if err != nil {
		t.Fatal(err)
	}
	if len(phone.Login) != loginLen {
		t.Fatalf("login %q", phone.Login)
	}
	login := phone.LoginAddress(u)
	if login != "inbox+"+phone.Login+"@reey.ru" {
		t.Errorf("login address %q", login)
	}

	for _, c := range []struct {
		login, password string
		ok              bool
	}{
		{"inbox@reey.ru", pw, true},     // default: the plain address
		{login, "phone password", true}, // its own login
		{strings.ToUpper(login), "phone password", true},
		{"inbox@reey.ru", "phone password", false}, // another password's login
		{login, pw, false},
		{"inbox+zzzz@reey.ru", pw, false}, // a login nobody has
	} {
		got, err := st.Authenticate(ctx, c.login, c.password)
		if c.ok && (err != nil || got.ID != u.ID) {
			t.Errorf("%s with %q: %v", c.login, c.password, err)
		}
		if !c.ok && err != ErrNotFound {
			t.Errorf("%s with %q signed in: %v", c.login, c.password, err)
		}
	}

	if _, err := st.AddCredential(ctx, u.ID, "iphone ", "another one"); err != nil {
		t.Errorf("labels compare as typed, so iphone is not iPhone: %v", err)
	}
	if _, err := st.AddCredential(ctx, u.ID, "iPhone", "another one"); !errors.Is(err, ErrConflict) {
		t.Errorf("a taken label: %v", err)
	}

	// The default goes like any other, and the plain address stops signing in.
	all, _ := st.Credentials(ctx)
	for _, p := range all[u.ID] {
		if p.Label == DefaultCredentialLabel {
			if err := st.DeleteCredential(ctx, u.ID, p.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := st.Authenticate(ctx, "inbox@reey.ru", pw); err != ErrNotFound {
		t.Errorf("the deleted default still signs in: %v", err)
	}
	if _, err := st.Authenticate(ctx, login, "phone password"); err != nil {
		t.Errorf("deleting the default took the phone's with it: %v", err)
	}
}

func TestAPasswordRecordsWhenItWasLastUsed(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	u := account(t, st, "inbox@reey.ru")
	before := st.Changes()
	if _, err := st.Authenticate(ctx, "inbox@reey.ru", pw); err != nil {
		t.Fatal(err)
	}
	all, _ := st.Credentials(ctx)
	if all[u.ID][0].LastUsedAt.IsZero() {
		t.Error("a sign-in left no trace")
	}
	if st.Changes() != before {
		t.Error("a sign-in counted as a change, which would schedule a backup")
	}
}

func TestIncomingMailIsKeptRefusedOrForwarded(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	inbox := account(t, st, "inbox@reey.ru")
	noreply := account(t, st, "noreply@reey.ru")
	billing := account(t, st, "billing@reey.ru")

	if err := st.SetIncoming(ctx, noreply.ID, IncomingReject, ""); err != nil {
		t.Fatal(err)
	}
	if r := resolve(t, st, "noreply+x@reey.ru"); !r.Ours || r.Account != nil || !r.Refused || r.Drop {
		t.Errorf("reject: %+v", r)
	}
	if err := st.SetIncoming(ctx, billing.ID, IncomingForward, inbox.ID); err != nil {
		t.Fatal(err)
	}
	if r := resolve(t, st, "billing@reey.ru"); r.Account == nil || r.Account.ID != inbox.ID || r.Refused {
		t.Errorf("forward: %+v", r)
	}
	// Forwarding into an address that refuses mail refuses it.
	if err := st.SetIncoming(ctx, inbox.ID, IncomingForward, noreply.ID); err != nil {
		t.Fatal(err)
	}
	if r := resolve(t, st, "billing@reey.ru"); r.Account != nil || !r.Refused {
		t.Errorf("forward to a refusing account: %+v", r)
	}
	// A circle is refused when it would be made.
	if err := st.SetIncoming(ctx, noreply.ID, IncomingForward, billing.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("a circle: %v", err)
	}
	for _, c := range []struct {
		in Incoming
		to string
	}{{IncomingForward, ""}, {IncomingKeep, inbox.ID}, {"bounce", ""}, {IncomingForward, billing.ID}} {
		if err := st.SetIncoming(ctx, billing.ID, c.in, c.to); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s to %q: %v", c.in, c.to, err)
		}
	}
	// An account others forward to stays while they do.
	if err := st.DeleteAccount(ctx, inbox.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("deleting a forward's target: %v", err)
	}
}

func TestADomainFollowsTheDefaultsUnlessItHasItsOwn(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	d, _ := st.CreateDomain(ctx, "reey.ru")
	set, err := st.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if set.DMARC != DMARCReject || set.SPF != SPFSoftFail {
		t.Fatalf("defaults %+v", set)
	}
	policies := func() string {
		d, _ := st.DomainByID(ctx, d.ID)
		set, _ := st.Settings(ctx)
		dmarc, spf := d.Policies(set)
		return string(dmarc) + " " + string(spf)
	}
	if got := policies(); got != "reject ~all" {
		t.Errorf("a new domain: %s", got)
	}
	st.SetDMARC(ctx, d.ID, DMARCQuarantine)
	st.SetSettings(ctx, Settings{Unknown: UnknownReject, DMARC: DMARCNone, SPF: SPFFail})
	if got := policies(); got != "quarantine -all" {
		t.Errorf("its own DMARC, the default SPF: %s", got)
	}
	st.SetDMARC(ctx, d.ID, "")
	if got := policies(); got != "none -all" {
		t.Errorf("back to the defaults: %s", got)
	}
	for _, bad := range []Settings{{DMARC: "bounce", SPF: SPFFail}, {DMARC: DMARCReject, SPF: "+all"}} {
		if err := st.SetSettings(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if err := st.SetSPF(ctx, d.ID, "+all"); !errors.Is(err, ErrInvalid) {
		t.Errorf("+all: %v", err)
	}
}

func TestADomainSendsThroughTheDefaultBuiltInOrItsOwnRelay(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sealer, _ := seal.New(make([]byte, seal.KeySize))
	st.SetSealer(sealer)
	d, _ := st.CreateDomain(ctx, "reey.ru")
	via := func(domain string) string {
		t.Helper()
		r, err := st.RelayFor(ctx, domain)
		if err != nil {
			t.Fatal(err)
		}
		if r == nil {
			return "built in"
		}
		return r.Label + " " + r.Password
	}
	if got := via("reey.ru"); got != "built in" {
		t.Errorf("no relay at all: %s", got)
	}
	gmail, err := st.CreateRelay(ctx, Relay{Label: "Gmail", Host: "smtp.gmail.com", Port: 587, Security: SecurityStartTLS, Username: "me", Password: "gmail secret"})
	if err != nil {
		t.Fatal(err)
	}
	yandex, _ := st.CreateRelay(ctx, Relay{Label: "Yandex", Host: "smtp.yandex.ru", Port: 465, Security: SecurityImplicit, Username: "r", Password: "yandex secret"})
	if _, err := st.CreateRelay(ctx, Relay{Label: "gmail", Host: "x.example", Port: 25, Security: SecurityStartTLS}); !errors.Is(err, ErrConflict) {
		t.Errorf("a taken label, another case: %v", err)
	}
	st.SetSettings(ctx, Settings{Unknown: UnknownReject, DMARC: DMARCReject, SPF: SPFSoftFail, RelayID: gmail.ID})
	if got := via("reey.ru"); got != "Gmail gmail secret" {
		t.Errorf("the default: %s", got)
	}
	st.SetDomainVia(ctx, d.ID, ViaBuiltIn)
	if got := via("reey.ru"); got != "built in" {
		t.Errorf("built in: %s", got)
	}
	st.SetDomainVia(ctx, d.ID, yandex.ID)
	if got := via("reey.ru"); got != "Yandex yandex secret" {
		t.Errorf("its own: %s", got)
	}
	if got := via("elsewhere.org"); got != "Gmail gmail secret" {
		t.Errorf("a domain mailio does not have: %s", got)
	}
	if err := st.SetDomainVia(ctx, d.ID, "rl_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a relay that does not exist: %v", err)
	}

	// A relay in use stays; the default too.
	if err := st.DeleteRelay(ctx, yandex.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("deleting a domain's relay: %v", err)
	}
	if err := st.DeleteRelay(ctx, gmail.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("deleting the default: %v", err)
	}
	st.SetDomainVia(ctx, d.ID, "")
	if got := via("reey.ru"); got != "Gmail gmail secret" {
		t.Errorf("back to the default: %s", got)
	}
	if err := st.DeleteRelay(ctx, yandex.ID); err != nil {
		t.Errorf("deleting an unused relay: %v", err)
	}

	// A port changed without the password keeps the password.
	if _, err := st.UpdateRelay(ctx, gmail.ID, Relay{Label: "Gmail", Host: "smtp.gmail.com", Port: 465, Security: SecurityImplicit, Username: "me"}); err != nil {
		t.Fatal(err)
	}
	if got := via("reey.ru"); got != "Gmail gmail secret" {
		t.Errorf("after a port change: %s", got)
	}
}

// The one relay there was before the list keeps its password, sealed the old way, and is sealed
// the new way when it is next saved.
func TestTheRelayFromBeforeTheListStillOpens(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	sealer, _ := seal.New(make([]byte, seal.KeySize))
	st.SetSealer(sealer)
	sealed := sealer.Seal([]byte("old secret"), []byte(legacyRelayAAD))
	if _, err := st.writer.Exec(`INSERT INTO relays (id, label, host, port, security, username, password_sealed, legacy_aad, created_at)
		VALUES ('rl_01m6z3hmy1v8qbhpactxqd87qk', 'smtp.gmail.com', 'smtp.gmail.com', 587, 'starttls', 'me', ?, 1, 1)`, sealed); err != nil {
		t.Fatal(err)
	}
	r, err := st.RelayByID(ctx, "rl_01m6z3hmy1v8qbhpactxqd87qk")
	if err != nil || r.Password != "old secret" {
		t.Fatalf("got %+v %v", r, err)
	}
	if _, err := st.UpdateRelay(ctx, r.ID, Relay{Label: "Gmail", Host: r.Host, Port: r.Port, Security: r.Security, Username: r.Username}); err != nil {
		t.Fatal(err)
	}
	if r, err := st.RelayByID(ctx, r.ID); err != nil || r.Password != "old secret" || r.Label != "Gmail" {
		t.Errorf("after saving: %+v %v", r, err)
	}
}
