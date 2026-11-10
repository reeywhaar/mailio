package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mailio/internal/address"
)

// An alias's mail goes to its account, tag and all, and the account can send as it. It has no
// credentials or mailboxes, and mail is sent on to it from nowhere: it only ever sends on itself.
func TestAnAliasDeliversToItsAccountWhichSendsAsIt(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	inbox := account(t, st, "inbox@example.org")
	other := account(t, st, "other@example.net")
	d, _ := st.DomainByName(ctx, "example.net")

	alias, err := st.CreateAlias(ctx, "shop@example.net", inbox.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !alias.Alias || alias.ForwardAccountID != inbox.ID || alias.Incoming != IncomingForward {
		t.Errorf("made: %+v", alias)
	}
	if r := resolve(t, st, "shop+promo@example.net"); r.Account == nil || r.Account.ID != inbox.ID || r.Tag != "promo" {
		t.Errorf("delivered to %+v", r)
	}
	a, _ := address.Parse("shop@example.net")
	if got, err := st.AliasOf(ctx, inbox.ID, a); err != nil || got.ID != alias.ID {
		t.Errorf("inbox sends as it: %v %v", got, err)
	}
	if _, err := st.AliasOf(ctx, other.ID, a); !errors.Is(err, ErrNotFound) {
		t.Errorf("somebody else sends as it: %v", err)
	}
	if _, err := st.MailboxNamed(ctx, alias.ID, Inbox); !errors.Is(err, ErrNotFound) {
		t.Errorf("it has an INBOX: %v", err)
	}
	if _, err := st.AddCredential(ctx, alias.ID, "phone", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("a credential for it: %v", err)
	}

	conflict := func(what string, err error, says string) {
		t.Helper()
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", what, err)
		}
	}
	_, err = st.CreateAlias(ctx, "deals@example.net", alias.ID)
	conflict("an alias of an alias", err, "shop@example.net is an alias")
	conflict("forwarding to it", st.SetIncoming(ctx, other.ID, IncomingForward, alias.ID), "is an alias")
	conflict("its catch-all", st.SetUnknown(ctx, d.ID, UnknownForward, alias.ID), "is an alias")
	conflict("deleting its account", st.DeleteAccount(ctx, inbox.ID), "shop@example.net is an alias of inbox@example.org")
	conflict("its account stops keeping", st.SetIncoming(ctx, inbox.ID, IncomingReject, ""), "is an alias of")
	if err := st.SetIncoming(ctx, alias.ID, IncomingKeep, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("an alias keeping: %v", err)
	}
	if _, err := st.CreateAlias(ctx, "inbox@example.org", other.ID); !errors.Is(err, ErrConflict) {
		t.Errorf("an address taken: %v", err)
	}

	// It moves to another account, and goes.
	if err := st.SetIncoming(ctx, alias.ID, IncomingForward, other.ID); err != nil {
		t.Fatal(err)
	}
	if r := resolve(t, st, "shop@example.net"); r.Account == nil || r.Account.ID != other.ID {
		t.Errorf("moved: %+v", r)
	}
	if err := st.DeleteAccount(ctx, alias.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteAccount(ctx, inbox.ID); err != nil {
		t.Errorf("its old account, with it gone: %v", err)
	}
}
