// Package e2e drives mailio's mail ports with real clients: SMTP in, IMAP out.
package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/smtp"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"mailio/internal/certs"
	"mailio/internal/imapd"
	"mailio/internal/smtpd"
	"mailio/internal/store"
)

func TestMain(m *testing.M) {
	store.SetBcryptCost(4)
	os.Exit(m.Run())
}

// verdict is what the fake checks say about every message.
type verdict struct {
	mu sync.Mutex
	v  smtpd.Verdict
}

func (f *verdict) Check(context.Context, net.IP, string, string, []byte) smtpd.Verdict {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.v
}

func (f *verdict) set(v smtpd.Verdict) {
	f.mu.Lock()
	f.v = v
	f.mu.Unlock()
}

type rig struct {
	t       *testing.T
	st      *store.Store
	smtp    string
	imap    string
	checks  *verdict
	log     *bytes.Buffer
	domain  *store.Domain
	account *store.Account
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var logged bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&syncWriter{w: &logged}, nil))

	d, err := st.CreateDomain(ctx, "reey.ru")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateAccount(ctx, "inbox@reey.ru", "correct horse")
	if err != nil {
		t.Fatal(err)
	}

	mgr, err := certs.New(ctx, st, "mail.test", log)
	if err != nil {
		t.Fatal(err)
	}
	imapSrv := imapd.New(st, log)
	imapLn, err := tls.Listen("tcp", "127.0.0.1:0", mgr.TLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	go imapSrv.Serve(imapLn)
	t.Cleanup(func() { imapSrv.Close() })

	checks := &verdict{v: smtpd.Verdict{SPF: "none", DKIM: "none", DMARC: "none"}}
	in := smtpd.NewInbound(st, log, "mail.test", checks).Server(mgr.TLSConfig())
	smtpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go in.Serve(smtpLn)
	t.Cleanup(func() { in.Close() })

	return &rig{t: t, st: st, smtp: smtpLn.Addr().String(), imap: imapLn.Addr().String(), checks: checks, log: &logged, domain: d, account: u}
}

type syncWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// send delivers one message over SMTP and returns the server's refusal, if any.
func (r *rig) send(from, to, body string) error {
	c, err := smtp.Dial(r.smtp)
	if err != nil {
		r.t.Fatal(err)
	}
	defer c.Close()
	if err := c.Hello("sender.test"); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (r *rig) client(h *imapclient.UnilateralDataHandler) *imapclient.Client {
	r.t.Helper()
	c, err := imapclient.DialTLS(r.imap, &imapclient.Options{
		TLSConfig:             &tls.Config{InsecureSkipVerify: true},
		UnilateralDataHandler: h,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	if err := c.Login("inbox@reey.ru", "correct horse").Wait(); err != nil {
		r.t.Fatal(err)
	}
	return c
}

const letter = "From: anna@example.com\r\nTo: inbox+shop@reey.ru\r\nSubject: Hello there\r\n\r\nA body line.\r\n"

func TestMailSentToATaggedAddressArrivesInTheInboxAndAnIdleClientHearsOfIt(t *testing.T) {
	r := newRig(t)
	exists := make(chan uint32, 4)
	c := r.client(&imapclient.UnilateralDataHandler{Mailbox: func(d *imapclient.UnilateralDataMailbox) {
		if d.NumMessages != nil {
			exists <- *d.NumMessages
		}
	}})
	sel, err := c.Select("INBOX", nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if sel.NumMessages != 0 {
		t.Fatalf("a new inbox has %d messages", sel.NumMessages)
	}
	idle, err := c.Idle()
	if err != nil {
		t.Fatal(err)
	}

	// A bare LF in the body is made CRLF on arrival.
	if err := r.send("anna@example.com", "inbox+shop@reey.ru", letter+"bare\nline\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-exists:
		if n != 1 {
			t.Errorf("EXISTS %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the idle client was not told")
	}
	if err := idle.Close(); err != nil {
		t.Fatal(err)
	}
	idle.Wait()

	msgs, err := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{
		Envelope: true, Flags: true, UID: true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}).Collect()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("fetch: %v %d", err, len(msgs))
	}
	m := msgs[0]
	if m.Envelope.Subject != "Hello there" {
		t.Errorf("subject %q", m.Envelope.Subject)
	}
	raw := string(m.BodySection[0].Bytes)
	for _, want := range []string{"Return-Path: <anna@example.com>", "Received: from sender.test", "by mail.test (mailio)", "bare\r\nline"} {
		if !strings.Contains(raw, want) {
			t.Errorf("the message lacks %q:\n%s", want, raw)
		}
	}
	if strings.Contains(raw, "bare\nline") {
		t.Error("a bare LF survived")
	}
	if strings.Contains(r.log.String(), "Hello there") {
		t.Error("the subject is in the log")
	}
}

func TestMailForNobodyOrSomebodyElseIsRefused(t *testing.T) {
	r := newRig(t)
	if err := r.send("anna@example.com", "nobody@reey.ru", letter); err == nil || !strings.Contains(err.Error(), "550") {
		t.Errorf("an unknown address: %v", err)
	}
	if err := r.send("anna@example.com", "someone@gmail.com", letter); err == nil || !strings.Contains(err.Error(), "550") {
		t.Errorf("relaying: %v", err)
	}
}

func TestUnknownMailIsJunkWhenTheDomainSaysSo(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.st.SetUnknown(ctx, r.domain.ID, store.UnknownJunk, r.account.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.send("anna@example.com", "whoever@reey.ru", letter); err != nil {
		t.Fatal(err)
	}
	c := r.client(nil)
	st, err := c.Status("Junk", &imap.StatusOptions{NumMessages: true}).Wait()
	if err != nil {
		t.Fatal(err)
	}
	if *st.NumMessages != 1 {
		t.Errorf("Junk has %d", *st.NumMessages)
	}

	if err := r.st.SetUnknown(ctx, r.domain.ID, store.UnknownDrop, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.send("anna@example.com", "whoever@reey.ru", letter); err != nil {
		t.Errorf("a dropped message is accepted: %v", err)
	}
	st, _ = c.Status("Junk", &imap.StatusOptions{NumMessages: true}).Wait()
	inbox, _ := c.Status("INBOX", &imap.StatusOptions{NumMessages: true}).Wait()
	if *st.NumMessages != 1 || *inbox.NumMessages != 0 {
		t.Errorf("a dropped message was kept: junk %d inbox %d", *st.NumMessages, *inbox.NumMessages)
	}
}

func TestADMARCRejectIsRefusedAndQuarantineIsJunk(t *testing.T) {
	r := newRig(t)
	r.checks.set(smtpd.Verdict{DMARC: "fail", Reject: "Rejected by the DMARC policy of example.com"})
	if err := r.send("anna@example.com", "inbox@reey.ru", letter); err == nil || !strings.Contains(err.Error(), "550") {
		t.Errorf("a DMARC reject: %v", err)
	}
	r.checks.set(smtpd.Verdict{DMARC: "fail", Junk: true})
	if err := r.send("anna@example.com", "inbox@reey.ru", letter); err != nil {
		t.Fatal(err)
	}
	c := r.client(nil)
	st, _ := c.Status("Junk", &imap.StatusOptions{NumMessages: true}).Wait()
	if *st.NumMessages != 1 {
		t.Errorf("a quarantined message is not in Junk: %d", *st.NumMessages)
	}
}

func TestAClientFlagsMovesAndExpunges(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 3; i++ {
		if err := r.send("anna@example.com", "inbox@reey.ru", letter); err != nil {
			t.Fatal(err)
		}
	}
	c := r.client(nil)
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}

	// A peek does not mark it read; a store does.
	msgs, _ := c.Fetch(imap.SeqSetNum(1), &imap.FetchOptions{Flags: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}}).Collect()
	if len(msgs) != 1 || len(msgs[0].Flags) != 0 {
		t.Fatalf("a peek set flags: %+v", msgs)
	}
	if _, err := c.Store(imap.SeqSetNum(2), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Expunge().Collect(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Move(imap.SeqSetNum(1), "Archive").Wait(); err != nil {
		t.Fatal(err)
	}

	inbox, _ := c.Status("INBOX", &imap.StatusOptions{NumMessages: true}).Wait()
	archive, _ := c.Status("Archive", &imap.StatusOptions{NumMessages: true}).Wait()
	if *inbox.NumMessages != 1 || *archive.NumMessages != 1 {
		t.Errorf("inbox %d archive %d, want 1 and 1", *inbox.NumMessages, *archive.NumMessages)
	}

	found, err := c.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "hello"}}}, nil).Wait()
	if err != nil || len(found.AllUIDs()) != 1 {
		t.Errorf("search: %v %v", err, found)
	}
}

func TestTheStandardMailboxesAreListedWithTheirUse(t *testing.T) {
	r := newRig(t)
	c := r.client(nil)
	list, err := c.List("", "*", nil).Collect()
	if err != nil {
		t.Fatal(err)
	}
	uses := make(map[string]bool)
	for _, l := range list {
		for _, a := range l.Attrs {
			uses[l.Mailbox+" "+string(a)] = true
		}
	}
	for _, want := range []string{`Sent \Sent`, `Junk \Junk`, `Trash \Trash`, `Drafts \Drafts`} {
		if !uses[want] {
			t.Errorf("missing %s in %v", want, uses)
		}
	}
}

func TestAWrongIMAPPasswordIsALineFail2banBansOn(t *testing.T) {
	r := newRig(t)
	c, err := imapclient.DialTLS(r.imap, &imapclient.Options{TLSConfig: &tls.Config{InsecureSkipVerify: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login("inbox@reey.ru", "wrong guess").Wait(); err == nil {
		t.Fatal("a wrong password signed in")
	}
	if !strings.Contains(r.log.String(), `"msg":"password refused","ip":"127.0.0.1","via":"imap"`) {
		t.Errorf("no fail2ban line in:\n%s", r.log.String())
	}
}

func TestASecondPasswordSignsInWithItsOwnLogin(t *testing.T) {
	r := newRig(t)
	p, err := r.st.AddCredential(context.Background(), r.account.ID, "laptop", "laptop password")
	if err != nil {
		t.Fatal(err)
	}
	login := p.LoginAddress(r.account)
	dial := func(login, password string) error {
		c, err := imapclient.DialTLS(r.imap, &imapclient.Options{TLSConfig: &tls.Config{InsecureSkipVerify: true}})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.Login(login, password).Wait()
	}
	if err := dial(login, "laptop password"); err != nil {
		t.Errorf("the laptop's login: %v", err)
	}
	if err := dial("inbox@reey.ru", "laptop password"); err == nil {
		t.Error("the laptop's password signed in under the plain address")
	}
	if err := dial("inbox@reey.ru", "correct horse"); err != nil {
		t.Errorf("the default: %v", err)
	}
}

func TestANoReplyAddressRefusesMailAndAForwardDeliversElsewhere(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	noreply, _ := r.st.CreateAccount(ctx, "noreply@reey.ru", "correct horse")
	billing, _ := r.st.CreateAccount(ctx, "billing@reey.ru", "correct horse")
	r.st.SetIncoming(ctx, noreply.ID, store.IncomingReject, "")
	r.st.SetIncoming(ctx, billing.ID, store.IncomingForward, r.account.ID)

	err := r.send("anna@example.com", "noreply@reey.ru", letter)
	if err == nil || !strings.Contains(err.Error(), "550") || !strings.Contains(err.Error(), "does not receive mail") {
		t.Errorf("no-reply: %v", err)
	}
	if err := r.send("anna@example.com", "billing+invoice@reey.ru", letter); err != nil {
		t.Fatal(err)
	}
	c := r.client(nil)
	st, _ := c.Status("INBOX", &imap.StatusOptions{NumMessages: true}).Wait()
	if *st.NumMessages != 1 {
		t.Errorf("the forward's target has %d", *st.NumMessages)
	}
}
