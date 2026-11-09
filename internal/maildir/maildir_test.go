package maildir

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mailio/internal/store"
)

func TestMain(m *testing.M) {
	store.SetBcryptCost(4)
	os.Exit(m.Run())
}

func write(t *testing.T, path, body string, at time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, at, at)
}

func TestASharedMaildirIsSortedToWhoEachMessageWasSentTo(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, d := range []string{"reey.ru", "vyrtsev.com"} {
		if _, err := st.CreateDomain(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	reey, _ := st.CreateUser(ctx, "inbox@reey.ru", "correct horse")
	vyr, _ := st.CreateUser(ctx, "inbox@vyrtsev.com", "correct horse")

	root := t.TempDir()
	old := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	write(t, filepath.Join(root, "cur", "1.a:2,S"), "X-Original-To: inbox+shop@reey.ru\r\nDelivered-To: root@mail.vyrtsev.com\r\nSubject: a\r\n\r\nx\r\n", old)
	write(t, filepath.Join(root, "new", "2.b"), "X-Original-To: inbox@vyrtsev.com\r\nSubject: b\r\n\r\nx\r\n", old.Add(time.Hour))
	write(t, filepath.Join(root, "cur", "3.c:2,FR"), "To: Someone <inbox@vyrtsev.com>\r\nSubject: c\r\n\r\nx\r\n", old.Add(2*time.Hour))
	write(t, filepath.Join(root, ".Sent", "cur", "4.d:2,S"), "From: inbox@reey.ru\r\nTo: anna@example.com\r\nSubject: d\r\n\r\nx\r\n", old)
	write(t, filepath.Join(root, ".Projects.2025", "cur", "5.e:2,S"), "X-Original-To: inbox@reey.ru\r\nSubject: e\r\n\r\nx\r\n", old)

	res, err := Import(ctx, st, root, []*store.User{reey, vyr}, true)
	if err != nil {
		t.Fatal(err)
	}
	if res["inbox@reey.ru INBOX"] != 1 || res["inbox@vyrtsev.com INBOX"] != 2 || res["inbox@reey.ru Sent"] != 1 || res["inbox@reey.ru Projects/2025"] != 1 {
		t.Fatalf("dry run: %v", res)
	}
	if box, _ := st.MailboxNamed(ctx, reey.ID, store.Inbox); box != nil {
		if msgs, _ := st.Messages(ctx, box.ID, nil); len(msgs) != 0 {
			t.Fatal("a dry run wrote")
		}
	}

	if _, err := Import(ctx, st, root, []*store.User{reey, vyr}, false); err != nil {
		t.Fatal(err)
	}
	box, _ := st.MailboxNamed(ctx, vyr.ID, store.Inbox)
	msgs, _ := st.Messages(ctx, box.ID, nil)
	if len(msgs) != 2 {
		t.Fatalf("vyrtsev inbox has %d", len(msgs))
	}
	// Order, flags and arrival time come from the files: new/ is unread, :2,FR is flagged and answered.
	if len(msgs[0].Flags) != 0 || !msgs[0].ReceivedAt.Equal(old.Add(time.Hour)) {
		t.Errorf("first: %+v", msgs[0])
	}
	if len(msgs[1].Flags) != 2 || msgs[1].Flags[0] != `\Answered` || msgs[1].Flags[1] != `\Flagged` {
		t.Errorf("second: %+v", msgs[1])
	}
	if _, err := st.MailboxNamed(ctx, reey.ID, "Projects/2025"); err != nil {
		t.Errorf("the nested folder was not made: %v", err)
	}
}
