package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"mailio/internal/seal"
)

// An account that sends as another goes out the way that one does, its own relay given up, while
// its own mail still comes to it. One step only, and the account sent as stays while it is.
func TestAnAccountSendsAsAnother(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	info := account(t, st, "info@example.org")
	inbox := account(t, st, "inbox@example.net")
	third := account(t, st, "third@example.org")
	sealer, _ := seal.New(make([]byte, seal.KeySize))
	st.SetSealer(sealer)
	relay, err := st.CreateRelay(ctx, Relay{Label: "Relay", Host: "smtp.example.net", Port: 587, Security: SecurityStartTLS, Username: "u", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccountVia(ctx, inbox.ID, relay.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccountVia(ctx, info.ID, ViaBuiltIn); err != nil {
		t.Fatal(err)
	}

	if err := st.SetAccountSendAs(ctx, info.ID, inbox.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.AccountByID(ctx, info.ID)
	if got.SendAs != inbox.ID || got.Via != "" || got.RelayID != "" {
		t.Errorf("sends as: %+v", got)
	}
	if r, err := st.RelayFor(ctx, info.ID, "example.org"); err != nil || r == nil || r.ID != relay.ID {
		t.Errorf("goes through %v, %v; want inbox's relay", r, err)
	}
	if r := resolve(t, st, "info@example.org"); r.Account == nil || r.Account.ID != info.ID {
		t.Errorf("its own mail went to %+v", r)
	}

	conflict := func(what string, err error, says string) {
		t.Helper()
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", what, err)
		}
	}
	conflict("sending as the one that sends as another", st.SetAccountSendAs(ctx, third.ID, info.ID), "info@example.org sends as another account itself")
	conflict("the one sent as sending as another", st.SetAccountSendAs(ctx, inbox.ID, third.ID), "info@example.org sends as inbox@example.net")
	conflict("deleting the one sent as", st.DeleteAccount(ctx, inbox.ID), "info@example.org sends as inbox@example.net")
	if err := st.SetAccountSendAs(ctx, third.ID, third.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("as itself: %v", err)
	}
	alias, err := st.CreateAlias(ctx, "shop@example.org", third.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccountSendAs(ctx, third.ID, alias.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("as an alias: %v", err)
	}
	if err := st.SetAccountSendAs(ctx, alias.ID, third.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("an alias sending as another: %v", err)
	}

	// A relay of its own sends as itself again, and the other can go.
	if err := st.SetAccountVia(ctx, info.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.AccountByID(ctx, info.ID); got.SendAs != "" {
		t.Errorf("still sends as %s", got.SendAs)
	}
	if err := st.DeleteAccount(ctx, inbox.ID); err != nil {
		t.Errorf("deleting it, with nobody sending as it: %v", err)
	}
}
