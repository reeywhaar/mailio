package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"mailio/internal/store"
)

func TestMain(m *testing.M) {
	store.SetBcryptCost(4)
	os.Exit(m.Run())
}

func openStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

var quiet = slog.New(slog.DiscardHandler)

func TestAnArchiveIsOneDatabaseSentWithItsSize(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t, dir)

	var received atomic.Int64
	var body atomic.Value
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		// Sized up front: an agent may refuse a chunked body.
		if r.ContentLength != int64(len(b)) || len(r.TransferEncoding) > 0 {
			t.Errorf("sent %d bytes as %d, %v", len(b), r.ContentLength, r.TransferEncoding)
		}
		body.Store(b)
		received.Add(1)
	}))
	defer agent.Close()

	if err := send(context.Background(), st, agent.URL, dir); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Fatalf("the agent got %d archives", received.Load())
	}
	raw, _ := body.Load().([]byte)
	if names := membersOf(t, raw); len(names) != 1 || names[0] != store.FileName {
		t.Errorf("archive holds %v", names)
	}
	// The pass cleans up after itself.
	if left, _ := filepath.Glob(filepath.Join(dir, scratch+"*")); len(left) > 0 {
		t.Errorf("left behind %v", left)
	}
}

// The counter is the signal: what somebody set up moves it, and the process noticing itself does
// not.
func TestOnlyContentCountsAsAChange(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, t.TempDir())

	moved := func(what string, f func() error) {
		t.Helper()
		before := st.Changes()
		if err := f(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if st.Changes() == before {
			t.Errorf("%s did not count as a change", what)
		}
	}
	still := func(what string, f func() error) {
		t.Helper()
		before := st.Changes()
		if err := f(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if st.Changes() != before {
			t.Errorf("%s counted as a change, which would schedule a backup", what)
		}
	}

	var link string
	still("an admin link", func() (err error) { link, _, err = st.CreateAdminLink(ctx); return })
	var admin *store.Admin
	moved("spending it", func() (err error) { admin, err = st.UseAdminLink(ctx, link, "misha", "correct horse"); return })
	still("a sign-in", func() error { _, err := st.CreateSession(ctx, "token", admin.ID, "curl/8"); return err })
	moved("an admin's password", func() error { return st.SetAdminPassword(ctx, admin.ID, "battery staple", "token") })
	var other *store.Admin
	moved("another admin", func() (err error) { other, err = st.CreateAdmin(ctx, "anna", "correct horse"); return })
	moved("an admin deleted", func() error { return st.DeleteAdmin(ctx, other.ID) })

	var d *store.Domain
	moved("a domain", func() (err error) { d, err = st.CreateDomain(ctx, "vyrtsev.com"); return })
	var u *store.Account
	moved("an account", func() (err error) { u, err = st.CreateAccount(ctx, "inbox@vyrtsev.com", "correct horse"); return })
	moved("a password", func() error { _, err := st.AddCredential(ctx, u.ID, "phone", "battery staple"); return err })
	moved("no-reply", func() error { return st.SetIncoming(ctx, u.ID, store.IncomingReject, "") })
	still("no-reply again", func() error { return st.SetIncoming(ctx, u.ID, store.IncomingReject, "") })
	moved("keeping its mail", func() error { return st.SetIncoming(ctx, u.ID, store.IncomingKeep, "") })
	moved("junk", func() error { return st.SetUnknown(ctx, d.ID, store.UnknownJunk, u.ID) })
	still("the same junk again", func() error { return st.SetUnknown(ctx, d.ID, store.UnknownJunk, u.ID) })
	moved("dropping instead", func() error { return st.SetUnknown(ctx, d.ID, store.UnknownDrop, "") })
	moved("an account deleted", func() error { return st.DeleteAccount(ctx, u.ID) })
	moved("a domain deleted", func() error { return st.DeleteDomain(ctx, d.ID) })
}

// A snapshot has to be a database rather than a copy of a moment that never existed.
func TestTheArchiveIsADatabaseThatOpens(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, t.TempDir())
	if _, err := st.CreateDomain(ctx, "reey.ru"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAccount(ctx, "inbox@reey.ru", "correct horse"); err != nil {
		t.Fatal(err)
	}

	restored := t.TempDir()
	if err := st.SnapshotTo(ctx, filepath.Join(restored, store.FileName)); err != nil {
		t.Fatal(err)
	}
	reopened := openStore(t, restored)
	if _, err := reopened.Authenticate(ctx, "inbox@reey.ru", "correct horse"); err != nil {
		t.Errorf("the restored database does not know the account: %v", err)
	}
}

func TestAFailingAgentIsReported(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t, dir)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer agent.Close()

	if err := send(context.Background(), st, agent.URL, dir); err == nil {
		t.Fatal("a refusing agent was reported as a success")
	}
}

func TestArchiveMembersAreNotDirectories(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t, dir)
	snapshot := filepath.Join(dir, "snap.db")
	if err := st.SnapshotTo(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "snap.tar.gz")
	if err := pack(snapshot, archive); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	tr := tarOf(t, raw)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeDir {
			t.Errorf("%s is a directory entry", h.Name)
		}
		if h.Mode != 0o600 {
			t.Errorf("%s is mode %o", h.Name, h.Mode)
		}
	}
}

// A pass the process died in leaves its snapshot behind; the next start removes it, backups on
// or off, and leaves the database alone.
func TestLeftoversAreRemovedOnStart(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t, dir)

	left := filepath.Join(dir, scratch+"123")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(left, store.FileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	Run(context.Background(), st, "", dir, quiet)

	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Errorf("the leftover is still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.FileName)); err != nil {
		t.Errorf("the database went with it: %v", err)
	}
}

func tarOf(t *testing.T, raw []byte) *tar.Reader {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return tar.NewReader(gz)
}

func membersOf(t *testing.T, raw []byte) []string {
	t.Helper()
	tr := tarOf(t, raw)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	return names
}
