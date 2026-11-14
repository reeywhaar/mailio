package store

import (
	"context"
	"testing"
)

// An account that does not send keeps receiving; choosing a relay, or another account to send as,
// has it send again.
func TestAnAccountThatDoesNotSendStillReceives(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	info := account(t, st, "info@example.org")
	inbox := account(t, st, "inbox@example.org")
	if err := st.SetAccountSendAs(ctx, info.ID, inbox.ID); err != nil {
		t.Fatal(err)
	}

	if err := st.SetAccountVia(ctx, info.ID, ViaNone); err != nil {
		t.Fatal(err)
	}
	got, _ := st.AccountByID(ctx, info.ID)
	if got.Via != ViaNone || got.SendAs != "" {
		t.Errorf("not sending: %+v", got)
	}
	if r := resolve(t, st, "info@example.org"); r.Account == nil || r.Account.ID != info.ID {
		t.Errorf("its mail went to %+v", r)
	}
	// What it queued before goes its domain's way, which is built-in here.
	if r, err := st.RelayFor(ctx, info.ID, "example.org"); err != nil || r != nil {
		t.Errorf("queued mail goes through %v, %v", r, err)
	}

	if err := st.SetAccountSendAs(ctx, info.ID, inbox.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.AccountByID(ctx, info.ID); got.Via != "" || got.SendAs != inbox.ID {
		t.Errorf("sending as inbox again: %+v", got)
	}
	if err := st.SetAccountVia(ctx, info.ID, ViaNone); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAccountVia(ctx, info.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.AccountByID(ctx, info.ID); got.Via != "" {
		t.Errorf("its domain's again: %+v", got)
	}
}
