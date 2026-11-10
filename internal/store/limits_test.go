package store

import (
	"context"
	"errors"
	"testing"

	"mailio/internal/address"
)

// The largest message an address takes is its account's own, else its domain's, else the
// default, and mail sent on has to fit the account it ends up at as well.
func TestTheLargestMessageIsTheAccountsOwnItsDomainsOrTheDefault(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	const mb = 1 << 20
	inbox := account(t, st, "inbox@example.org")
	billing := account(t, st, "billing@example.org")
	d, _ := st.DomainByName(ctx, "example.org")
	limit := func(addr string) int64 {
		t.Helper()
		a, err := address.Parse(addr)
		if err != nil {
			t.Fatal(err)
		}
		r, err := st.Resolve(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		return r.Limit
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	if inbox.MessageLimit != DefaultMessageBytes || limit("inbox@example.org") != DefaultMessageBytes {
		t.Errorf("a new account: %d, %d", inbox.MessageLimit, limit("inbox@example.org"))
	}
	set, _ := st.Settings(ctx)
	set.MaxMessageBytes = 40 * mb
	must(st.SetSettings(ctx, *set))
	if got := limit("inbox+shop@example.org"); got != 40*mb {
		t.Errorf("the default: %d", got)
	}
	must(st.SetDomainMessageLimit(ctx, d.ID, 30*mb))
	if got := limit("inbox@example.org"); got != 30*mb {
		t.Errorf("its domain's: %d", got)
	}
	must(st.SetAccountMessageLimit(ctx, inbox.ID, 80*mb))
	if got := limit("inbox@example.org"); got != 80*mb {
		t.Errorf("its own, above its domain's: %d", got)
	}

	must(st.SetIncoming(ctx, billing.ID, IncomingForward, inbox.ID))
	must(st.SetAccountMessageLimit(ctx, billing.ID, 90*mb))
	if got := limit("billing@example.org"); got != 80*mb {
		t.Errorf("forwarded to a smaller one: %d", got)
	}
	must(st.SetAccountMessageLimit(ctx, billing.ID, 0))
	if got := limit("billing@example.org"); got != 30*mb {
		t.Errorf("forwarding, on its domain's: %d", got)
	}

	must(st.SetUnknown(ctx, d.ID, UnknownJunk, inbox.ID))
	if got := limit("nobody@example.org"); got != 30*mb {
		t.Errorf("junk, on its domain's: %d", got)
	}
	must(st.SetAccountMessageLimit(ctx, inbox.ID, 10*mb))
	if got := limit("nobody@example.org"); got != 10*mb {
		t.Errorf("junk to a smaller one: %d", got)
	}

	must(st.SetDomainMessageLimit(ctx, d.ID, 0))
	must(st.SetAccountMessageLimit(ctx, inbox.ID, 0))
	a, _ := st.AccountByID(ctx, inbox.ID)
	if a.MaxMessageBytes != 0 || a.MessageLimit != 40*mb {
		t.Errorf("back to the default: own %d, applies %d", a.MaxMessageBytes, a.MessageLimit)
	}

	for _, bad := range []int64{-1, 1000, MessageCeiling + 1} {
		if err := st.SetAccountMessageLimit(ctx, inbox.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("an account's %d: %v", bad, err)
		}
		if err := st.SetDomainMessageLimit(ctx, d.ID, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("a domain's %d: %v", bad, err)
		}
	}
	if err := st.SetAccountMessageLimit(ctx, "ac_01jnothere0000000000000000", 10*mb); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such account: %v", err)
	}
}
