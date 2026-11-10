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
		n, junk, err := st.EmptyTrashAndJunk(ctx)
		if err != nil || junk != 0 {
			t.Fatalf("%d from Junk: %v", junk, err)
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
	must(st.SetDomainKept(ctx, d.ID, Trash, &week))
	trashIt()
	if n := after(7); n != 1 {
		t.Errorf("7 days, the domain's: %d went", n)
	}
	must(st.SetAccountKept(ctx, inbox.ID, Trash, &forever))
	trashIt()
	if n := after(1000); n != 0 {
		t.Errorf("forever, the account's: %d went", n)
	}
	must(st.SetAccountKept(ctx, inbox.ID, Trash, nil))
	if n := after(0); n != 1 {
		t.Errorf("back to the domain's: %d went", n)
	}

	for _, bad := range []int{-1, maxKeptDays + 1} {
		if err := st.SetAccountKept(ctx, inbox.ID, Trash, &bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("an account's %d: %v", bad, err)
		}
		if err := st.SetDomainKept(ctx, d.ID, Junk, &bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("a domain's %d: %v", bad, err)
		}
	}
}

// Junk keeps a message as Trash does, by days of its own, and emptying one leaves the other.
func TestJunkKeepsAMessageItsOwnDays(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	st.SetClock(func() time.Time { return now })
	inbox := account(t, st, "inbox@example.org")
	junk, _ := st.SpecialMailbox(ctx, inbox.ID, `\Junk`)
	trash, _ := st.SpecialMailbox(ctx, inbox.ID, `\Trash`)
	raw := []byte("Subject: offer\r\n\r\nbody\r\n")
	if err := st.Deliver(ctx, raw, now, []Delivery{{MailboxID: junk.ID}, {MailboxID: trash.ID}}); err != nil {
		t.Fatal(err)
	}
	three := 3
	if err := st.SetAccountKept(ctx, inbox.ID, Junk, &three); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 3)
	gone, went, err := st.EmptyTrashAndJunk(ctx)
	if err != nil || gone != 0 || went != 1 {
		t.Errorf("after 3 days: %d from Trash, %d from Junk, %v", gone, went, err)
	}
	if uids, _ := st.UIDs(ctx, trash.ID); len(uids) != 1 {
		t.Errorf("Trash has %d", len(uids))
	}
	a, _ := st.AccountByID(ctx, inbox.ID)
	if a.JunkDays == nil || *a.JunkDays != 3 || a.TrashDays != nil {
		t.Errorf("its own: junk %v, trash %v", a.JunkDays, a.TrashDays)
	}
}

// Clearing an account's mail deletes every message it holds and keeps its mailboxes; another
// account's mail stays, and the listener hears of each one.
func TestClearingAnAccountsMailKeepsItsMailboxes(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	h := &heard{expunged: map[int64][]uint32{}}
	st.SetMailListener(h)
	inbox := account(t, st, "inbox@example.org")
	other := account(t, st, "other@example.org")
	box, _ := st.MailboxNamed(ctx, inbox.ID, Inbox)
	sent, _ := st.SpecialMailbox(ctx, inbox.ID, `\Sent`)
	theirs, _ := st.MailboxNamed(ctx, other.ID, Inbox)
	raw := []byte("Subject: hi\r\n\r\nbody\r\n")
	for _, to := range [][]Delivery{{{MailboxID: box.ID}, {MailboxID: sent.ID}}, {{MailboxID: box.ID}, {MailboxID: theirs.ID}}} {
		if err := st.Deliver(ctx, raw, time.Now(), to); err != nil {
			t.Fatal(err)
		}
	}
	if stored, _ := st.StoredMail(ctx); stored[inbox.ID] != (Stored{3, 3 * int64(len(raw))}) || stored[other.ID].Messages != 1 {
		t.Fatalf("stored before: %v", stored)
	}

	n, err := st.ClearMail(ctx, inbox.ID)
	if err != nil || n != 3 {
		t.Fatalf("cleared %d: %v", n, err)
	}
	stored, _ := st.StoredMail(ctx)
	if stored[inbox.ID] != (Stored{}) || stored[other.ID].Messages != 1 {
		t.Errorf("stored after: %v", stored)
	}
	if boxes, _ := st.Mailboxes(ctx, inbox.ID); len(boxes) != len(StandardMailboxes) {
		t.Errorf("%d mailboxes left", len(boxes))
	}
	if len(h.expunged[box.ID]) != 2 || len(h.expunged[sent.ID]) != 1 {
		t.Errorf("the listener heard %v", h.expunged)
	}
	if _, err := st.ClearMail(ctx, "ac_01jnothere0000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such account: %v", err)
	}
}
