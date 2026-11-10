package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// heard is a listener that writes down what it is told was deleted.
type heard struct{ expunged map[int64][]uint32 }

func (h *heard) Appended(int64, uint32) {}
func (h *heard) Expunged(box int64, uids []uint32) {
	h.expunged[box] = append(h.expunged[box], uids...)
}

// Trash keeps a message the account's days, its domain's or the default's, counted from when it
// was put there, then deletes it for good; 0 keeps it forever, and no other mailbox is emptied.
func TestTrashKeepsAMessageItsDaysThenItGoes(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	st.SetClock(func() time.Time { return now })
	h := &heard{expunged: map[int64][]uint32{}}
	st.SetMailListener(h)
	inbox := account(t, st, "inbox@example.org")
	d, _ := st.DomainByName(ctx, "example.org")
	box, _ := st.MailboxNamed(ctx, inbox.ID, Inbox)
	trash, err := st.SpecialMailbox(ctx, inbox.ID, `\Trash`)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	// It arrived a year ago and is put in Trash now: the year does not count.
	must(st.Deliver(ctx, []byte("Subject: old\r\n\r\nbody\r\n"), now.AddDate(-1, 0, 0), []Delivery{{MailboxID: box.ID}}))
	trashIt := func() {
		t.Helper()
		uids, _ := st.UIDs(ctx, box.ID)
		if _, _, err := st.Copy(ctx, box.ID, uids, trash.ID); err != nil {
			t.Fatal(err)
		}
	}
	after := func(days int) int {
		t.Helper()
		now = now.AddDate(0, 0, days)
		n, err := st.EmptyTrash(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	trashIt()
	if n := after(29); n != 0 {
		t.Errorf("29 days, by default: %d went", n)
	}
	if n := after(1); n != 1 {
		t.Errorf("30 days, by default: %d went", n)
	}
	if uids, _ := st.UIDs(ctx, box.ID); len(uids) != 1 {
		t.Errorf("INBOX was emptied too: %d left", len(uids))
	}
	if len(h.expunged[trash.ID]) != 1 {
		t.Errorf("the listener heard %v", h.expunged)
	}

	week, forever := 7, 0
	must(st.SetDomainTrashDays(ctx, d.ID, &week))
	trashIt()
	if n := after(7); n != 1 {
		t.Errorf("7 days, the domain's: %d went", n)
	}
	must(st.SetAccountTrashDays(ctx, inbox.ID, &forever))
	trashIt()
	if n := after(1000); n != 0 {
		t.Errorf("forever, the account's: %d went", n)
	}
	must(st.SetAccountTrashDays(ctx, inbox.ID, nil))
	if n := after(0); n != 1 {
		t.Errorf("back to the domain's: %d went", n)
	}

	for _, bad := range []int{-1, maxTrashDays + 1} {
		if err := st.SetAccountTrashDays(ctx, inbox.ID, &bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("an account's %d: %v", bad, err)
		}
		if err := st.SetDomainTrashDays(ctx, d.ID, &bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("a domain's %d: %v", bad, err)
		}
	}
}
