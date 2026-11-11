package outbound

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"mailio/internal/certs"
	"mailio/internal/dkimkey"
	"mailio/internal/seal"
	"mailio/internal/smtpd"
	"mailio/internal/store"
)

func TestMain(m *testing.M) {
	store.SetBcryptCost(4)
	os.Exit(m.Run())
}

// relay is a fake relay: STARTTLS, a password, and an answer per recipient.
type relay struct {
	mu       sync.Mutex
	got      []received
	refuse   map[string]*gosmtp.SMTPError
	username string
	password string
}

type received struct {
	from  string
	rcpts []string
	data  []byte
}

func (r *relay) NewSession(*gosmtp.Conn) (gosmtp.Session, error) { return &relaySession{r: r}, nil }

type relaySession struct {
	r      *relay
	authed bool
	cur    received
}

func (s *relaySession) AuthMechanisms() []string { return []string{sasl.Plain} }
func (s *relaySession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, u, p string) error {
		if u != s.r.username || p != s.r.password {
			return gosmtp.ErrAuthFailed
		}
		s.authed = true
		return nil
	}), nil
}
func (s *relaySession) Mail(from string, _ *gosmtp.MailOptions) error {
	if !s.authed {
		return gosmtp.ErrAuthRequired
	}
	s.cur = received{from: from}
	return nil
}
func (s *relaySession) Rcpt(to string, _ *gosmtp.RcptOptions) error {
	if e := s.r.refuse[to]; e != nil {
		return e
	}
	s.cur.rcpts = append(s.cur.rcpts, to)
	return nil
}
func (s *relaySession) Data(r io.Reader) error {
	s.cur.data, _ = io.ReadAll(r)
	s.r.mu.Lock()
	s.r.got = append(s.r.got, s.cur)
	s.r.mu.Unlock()
	return nil
}
func (s *relaySession) Reset()        {}
func (s *relaySession) Logout() error { return nil }

type rig struct {
	t      *testing.T
	st     *store.Store
	sender *Sender
	relay  *relay
	sub    string
	log    *bytes.Buffer
	domain *store.Domain
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, _ := seal.New(make([]byte, seal.KeySize))
	st.SetSealer(sealer)
	var logged bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logged, nil))

	d, err := st.CreateDomain(ctx, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"inbox@example.org", "other@example.org"} {
		if _, err := st.CreateAccount(ctx, a, "correct horse"); err != nil {
			t.Fatal(err)
		}
	}

	// The relay, with a certificate the sender is told to trust.
	cert, _ := certs.SelfSigned("localhost")
	pair, _ := tls.X509KeyPair([]byte(cert.Chain), []byte(cert.Key))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(cert.Chain))
	fake := &relay{refuse: map[string]*gosmtp.SMTPError{}, username: "relayuser", password: "relaypass"}
	rs := gosmtp.NewServer(fake)
	rs.Domain = "localhost"
	rs.TLSConfig = &tls.Config{Certificates: []tls.Certificate{pair}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go rs.Serve(ln)
	t.Cleanup(func() { rs.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	rl, err := st.CreateRelay(ctx, store.Relay{Label: "test relay", Host: "localhost", Port: port,
		Security: store.SecurityStartTLS, Username: "relayuser", Password: "relaypass"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettings(ctx, store.Settings{Unknown: store.UnknownReject, DMARC: store.DMARCReject, SPF: store.SPFSoftFail, RelayID: rl.ID, MaxMessageBytes: store.DefaultMessageBytes}); err != nil {
		t.Fatal(err)
	}

	sender := New(st, log, "mail.test")
	sender.roots = roots

	mgr, err := certs.New(ctx, st, "mail.test", log)
	if err != nil {
		t.Fatal(err)
	}
	sub := smtpd.NewSubmission(st, log, "mail.test", sender).Server(mgr.TLSConfig())
	subLn, err := tls.Listen("tcp", "127.0.0.1:0", mgr.TLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	go sub.Serve(subLn)
	t.Cleanup(func() { sub.Close() })

	return &rig{t: t, st: st, sender: sender, relay: fake, sub: subLn.Addr().String(), log: &logged, domain: d}
}

// submit sends as an account over 465, returning the first refusal.
func (r *rig) submit(login, password, from string, to []string, body string) error {
	conn, err := tls.Dial("tcp", r.sub, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		r.t.Fatal(err)
	}
	c, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		r.t.Fatal(err)
	}
	defer c.Close()
	if err := c.Auth(smtp.PlainAuth("", login, password, "localhost")); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	w.Write([]byte(body))
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

const letter = "From: Misha <inbox@example.org>\r\nTo: someone@example.com\r\nSubject: Hi\r\n\r\nHello.\r\n"

func (r *rig) inbox(addr string) []string {
	r.t.Helper()
	ctx := context.Background()
	u, err := r.st.Authenticate(ctx, addr, "correct horse")
	if err != nil {
		r.t.Fatal(err)
	}
	box, _ := r.st.MailboxNamed(ctx, u.ID, store.Inbox)
	msgs, _ := r.st.Messages(ctx, box.ID, nil)
	var out []string
	for _, m := range msgs {
		raw, _ := r.st.Body(ctx, box.ID, m.UID)
		out = append(out, string(raw))
	}
	return out
}

func TestSubmittedMailGoesThroughTheRelaySignedAndLocalMailStaysHome(t *testing.T) {
	r := newRig(t)
	if err := r.submit("inbox@example.org", "correct horse", "inbox+news@example.org",
		[]string{"someone@example.com", "other@example.org"}, letter); err != nil {
		t.Fatal(err)
	}
	r.sender.runOnce(context.Background())

	if len(r.relay.got) != 1 {
		t.Fatalf("the relay got %d messages", len(r.relay.got))
	}
	got := r.relay.got[0]
	if got.from != "inbox+news@example.org" || len(got.rcpts) != 1 || got.rcpts[0] != "someone@example.com" {
		t.Errorf("the relay got %s → %v", got.from, got.rcpts)
	}
	// The signature verifies against the record mailio publishes.
	record, _ := dkimkey.Record(r.domain.DKIMKey)
	vs, err := dkim.VerifyWithOptions(bytes.NewReader(got.data), &dkim.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) {
			if name != dkimkey.RecordName("example.org", dkimkey.Selector) {
				t.Errorf("looked up %s", name)
			}
			return []string{record}, nil
		},
	})
	if err != nil || len(vs) != 1 || vs[0].Err != nil || vs[0].Domain != "example.org" {
		t.Errorf("dkim: %v %+v", err, vs)
	}
	if !strings.Contains(string(got.data), "Message-ID: <") || !strings.Contains(string(got.data), "Date: ") {
		t.Error("Message-ID or Date was not added")
	}

	if local := r.inbox("other@example.org"); len(local) != 1 || !strings.Contains(local[0], "Subject: Hi") {
		t.Errorf("the local recipient got %d", len(local))
	}
	if q, _ := r.st.Outbound(context.Background()); len(q) != 0 {
		t.Errorf("the queue still holds %d", len(q))
	}
}

func TestAnAccountSendsOnlyAsItself(t *testing.T) {
	r := newRig(t)
	err := r.submit("inbox@example.org", "correct horse", "other@example.org", []string{"someone@example.com"}, letter)
	if err == nil || !strings.Contains(err.Error(), "553") {
		t.Errorf("sending as somebody else: %v", err)
	}
	err = r.submit("inbox@example.org", "wrong guess", "inbox@example.org", []string{"someone@example.com"}, letter)
	if err == nil {
		t.Error("a wrong password sent mail")
	}
	if !strings.Contains(r.log.String(), `"msg":"password refused","ip":"127.0.0.1","via":"submission"`) {
		t.Errorf("no fail2ban line:\n%s", r.log.String())
	}
}

func TestARefusedRecipientIsReportedAndATemporaryOneRetried(t *testing.T) {
	r := newRig(t)
	r.relay.refuse["bad@example.com"] = &gosmtp.SMTPError{Code: 550, EnhancedCode: gosmtp.EnhancedCode{5, 1, 1}, Message: "No such user"}
	r.relay.refuse["later@example.com"] = &gosmtp.SMTPError{Code: 451, EnhancedCode: gosmtp.EnhancedCode{4, 3, 0}, Message: "Try later"}
	if err := r.submit("inbox@example.org", "correct horse", "inbox@example.org",
		[]string{"bad@example.com", "later@example.com"}, letter); err != nil {
		t.Fatal(err)
	}
	r.sender.runOnce(context.Background())

	bounces := r.inbox("inbox@example.org")
	if len(bounces) != 1 || !strings.Contains(bounces[0], "Undelivered mail to bad@example.com") || !strings.Contains(bounces[0], "No such user") {
		t.Errorf("bounces: %v", bounces)
	}
	q, _ := r.st.Outbound(context.Background())
	if len(q) != 1 || q[0].Recipient != "later@example.com" || q[0].Attempts != 1 || !q[0].NextAt.After(time.Now()) {
		t.Errorf("queue: %+v", q)
	}
}

func TestAWrongRelayPasswordIsRetriedNotBounced(t *testing.T) {
	r := newRig(t)
	r.relay.password = "changed"
	if err := r.submit("inbox@example.org", "correct horse", "inbox@example.org", []string{"someone@example.com"}, letter); err != nil {
		t.Fatal(err)
	}
	r.sender.runOnce(context.Background())
	if q, _ := r.st.Outbound(context.Background()); len(q) != 1 || q[0].LastError == "" {
		t.Errorf("queue: %+v", q)
	}
}

// A domain set to built-in goes to the recipient's MX, whatever the default relay.
func TestADomainThatSendsDirectlySkipsTheDefaultRelay(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if err := r.st.SetDomainVia(ctx, r.domain.ID, store.ViaBuiltIn); err != nil {
		t.Fatal(err)
	}
	// The recipient's MX is a fake on a local port.
	mx := &relay{refuse: map[string]*gosmtp.SMTPError{}}
	srv := gosmtp.NewServer(&openMX{mx})
	srv.Domain = "mx.example.com"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	r.sender.mx = func(context.Context, string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mx.example.com.", Pref: 10}}, nil
	}
	dial := r.sender.dial
	r.sender.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "mx.example.com:25" {
			addr = ln.Addr().String()
		}
		return dial(ctx, network, addr)
	}

	if err := r.submit("inbox@example.org", "correct horse", "inbox@example.org", []string{"someone@example.com"}, letter); err != nil {
		t.Fatal(err)
	}
	r.sender.runOnce(ctx)
	if len(r.relay.got) != 0 {
		t.Errorf("the default relay got %d", len(r.relay.got))
	}
	if len(mx.got) != 1 || mx.got[0].rcpts[0] != "someone@example.com" {
		t.Errorf("the MX got %+v", mx.got)
	}
}

// openMX is the fake relay without its password: an MX takes mail from anyone.
type openMX struct{ r *relay }

func (m *openMX) NewSession(*gosmtp.Conn) (gosmtp.Session, error) {
	return &relaySession{r: m.r, authed: true}, nil
}

// An account sends nothing larger than its own limit, nor larger than a local recipient takes,
// and its client hears so at once rather than from a bounce later.
func TestASubmissionLargerThanAllowedIsRefused(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	big := letter + strings.Repeat("A line of an attachment, encoded the way mail carries one.\r\n", 20000)
	inbox, _ := r.st.Authenticate(ctx, "inbox@example.org", "correct horse")
	other, _ := r.st.Authenticate(ctx, "other@example.org", "correct horse")

	if err := r.st.SetAccountMessageLimit(ctx, other.ID, 1<<20); err != nil {
		t.Fatal(err)
	}
	err := r.submit("inbox@example.org", "correct horse", "inbox@example.org", []string{"someone@example.com", "other@example.org"}, big)
	if err == nil || !strings.Contains(err.Error(), "552") || !strings.Contains(err.Error(), "other@example.org") {
		t.Errorf("a local recipient's: %v", err)
	}
	if err := r.submit("inbox@example.org", "correct horse", "inbox@example.org", []string{"someone@example.com"}, big); err != nil {
		t.Errorf("within the sender's: %v", err)
	}

	if err := r.st.SetAccountMessageLimit(ctx, inbox.ID, 1<<20); err != nil {
		t.Fatal(err)
	}
	err = r.submit("inbox@example.org", "correct horse", "inbox@example.org", []string{"someone@example.com"}, big)
	if err == nil || !strings.Contains(err.Error(), "552") || !strings.Contains(err.Error(), "inbox@example.org sends up to 1 MB") {
		t.Errorf("the sender's own: %v", err)
	}
}

// An account's own relay carries its mail, over its domain's built-in.
func TestAnAccountsOwnRelayCarriesItsMail(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	inbox, _ := r.st.Authenticate(ctx, "inbox@example.org", "correct horse")
	set, _ := r.st.Settings(ctx)
	if err := r.st.SetDomainVia(ctx, r.domain.ID, store.ViaBuiltIn); err != nil {
		t.Fatal(err)
	}
	if err := r.st.SetAccountVia(ctx, inbox.ID, set.RelayID); err != nil {
		t.Fatal(err)
	}
	if err := r.submit("inbox@example.org", "correct horse", "inbox@example.org", []string{"someone@example.com"}, letter); err != nil {
		t.Fatal(err)
	}
	r.sender.runOnce(ctx)
	if len(r.relay.got) != 1 {
		t.Errorf("the relay got %d messages", len(r.relay.got))
	}
}

// An account sends as its alias, signed for the alias's domain, and as nobody else's.
func TestAnAccountSendsAsItsAliasSignedForTheAliasesDomain(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	inbox, _ := r.st.Authenticate(ctx, "inbox@example.org", "correct horse")
	other, _ := r.st.Authenticate(ctx, "other@example.org", "correct horse")
	vy, err := r.st.CreateDomain(ctx, "example.net")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.CreateAlias(ctx, "shop@example.net", inbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.st.CreateAlias(ctx, "billing@example.net", other.ID); err != nil {
		t.Fatal(err)
	}

	body := "From: Shop <shop@example.net>\r\nTo: someone@example.com\r\nSubject: Hi\r\n\r\nHello.\r\n"
	if err := r.submit("inbox@example.org", "correct horse", "shop+news@example.net", []string{"someone@example.com"}, body); err != nil {
		t.Fatal(err)
	}
	err = r.submit("inbox@example.org", "correct horse", "billing@example.net", []string{"someone@example.com"}, body)
	if err == nil || !strings.Contains(err.Error(), "553") {
		t.Errorf("as somebody else's alias: %v", err)
	}
	r.sender.runOnce(ctx)

	if len(r.relay.got) != 1 {
		t.Fatalf("the relay got %d messages", len(r.relay.got))
	}
	got := r.relay.got[0]
	if got.from != "shop+news@example.net" {
		t.Errorf("sent from %s", got.from)
	}
	record, _ := dkimkey.Record(vy.DKIMKey)
	vs, err := dkim.VerifyWithOptions(bytes.NewReader(got.data), &dkim.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) {
			if name != dkimkey.RecordName("example.net", dkimkey.Selector) {
				t.Errorf("looked up %s", name)
			}
			return []string{record}, nil
		},
	})
	if err != nil || len(vs) != 1 || vs[0].Err != nil || vs[0].Domain != "example.net" {
		t.Errorf("dkim: %v %+v", err, vs)
	}
}

// submitHeld sends as an account over 465 by hand, MAIL FROM carrying hold, an argument such as
// HOLDFOR=3600, which net/smtp cannot send. It returns the EHLO answer and the first refusal.
func (r *rig) submitHeld(login, password, from, hold string, to []string, body string) (string, error) {
	r.t.Helper()
	conn, err := tls.Dial("tcp", r.sub, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		r.t.Fatal(err)
	}
	defer conn.Close()
	c := textproto.NewConn(conn)
	if _, _, err := c.ReadResponse(220); err != nil {
		return "", err
	}
	step := func(code int, format string, args ...any) (string, error) {
		id, err := c.Cmd(format, args...)
		if err != nil {
			return "", err
		}
		c.StartResponse(id)
		defer c.EndResponse(id)
		_, msg, err := c.ReadResponse(code)
		return msg, err
	}
	ehlo, err := step(250, "EHLO localhost")
	if err != nil {
		return "", err
	}
	auth := base64.StdEncoding.EncodeToString([]byte("\x00" + login + "\x00" + password))
	if _, err := step(235, "AUTH PLAIN %s", auth); err != nil {
		return ehlo, err
	}
	if _, err := step(250, "MAIL FROM:<%s> %s", from, hold); err != nil {
		return ehlo, err
	}
	for _, rcpt := range to {
		if _, err := step(250, "RCPT TO:<%s>", rcpt); err != nil {
			return ehlo, err
		}
	}
	if _, err := step(354, "DATA"); err != nil {
		return ehlo, err
	}
	w := c.DotWriter()
	w.Write([]byte(body))
	w.Close()
	if _, _, err := c.ReadResponse(250); err != nil {
		return ehlo, err
	}
	return ehlo, nil
}

// A message held until a time the client chose waits in the queue, there and here alike, Retry
// now included, and then goes: to the relay, and to the mailbox here.
func TestAHeldMessageGoesAtItsTime(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	ehlo, err := r.submitHeld("inbox@example.org", "correct horse", "inbox@example.org", "HOLDFOR=3600",
		[]string{"someone@example.com", "other@example.org"}, letter)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ehlo, "FUTURERELEASE 31536000 ") {
		t.Errorf("EHLO says:\n%s", ehlo)
	}

	r.sender.runOnce(ctx)
	if err := r.st.RetryOutbound(ctx); err != nil {
		t.Fatal(err)
	}
	r.sender.runOnce(ctx)
	if len(r.relay.got) != 0 || len(r.inbox("other@example.org")) != 0 {
		t.Fatalf("sent before its time: relay %d, here %d", len(r.relay.got), len(r.inbox("other@example.org")))
	}
	q, _ := r.st.Outbound(ctx)
	if len(q) != 2 || time.Until(q[0].ReleaseAt) < 59*time.Minute {
		t.Fatalf("queued: %+v", q)
	}

	r.st.SetClock(func() time.Time { return time.Now().Add(time.Hour + time.Minute) })
	r.sender.runOnce(ctx)
	if len(r.relay.got) != 1 || r.relay.got[0].rcpts[0] != "someone@example.com" {
		t.Errorf("the relay got %+v", r.relay.got)
	}
	if here := r.inbox("other@example.org"); len(here) != 1 {
		t.Errorf("here: %d", len(here))
	}
	if q, _ := r.st.Outbound(ctx); len(q) != 0 {
		t.Errorf("the queue holds %d", len(q))
	}

	// Longer than mailio holds mail is refused while the client can still say so.
	if _, err := r.submitHeld("inbox@example.org", "correct horse", "inbox@example.org", "HOLDFOR=40000000",
		[]string{"someone@example.com"}, letter); err == nil || !strings.Contains(err.Error(), "501") {
		t.Errorf("held two years: %v", err)
	}
}

// mailbox is what one of an account's mailboxes holds: each message's bytes and arrival.
func (r *rig) mailbox(addr, name string) ([]string, []time.Time) {
	r.t.Helper()
	ctx := context.Background()
	u, err := r.st.Authenticate(ctx, addr, "correct horse")
	if err != nil {
		r.t.Fatal(err)
	}
	box, err := r.st.MailboxNamed(ctx, u.ID, name)
	if err != nil {
		r.t.Fatal(err)
	}
	msgs, _ := r.st.Messages(ctx, box.ID, nil)
	var raws []string
	var at []time.Time
	for _, m := range msgs {
		raw, _ := r.st.Body(ctx, box.ID, m.UID)
		raws = append(raws, string(raw))
		at = append(at, m.ReceivedAt)
	}
	return raws, at
}

// A held message shows in its sender's Scheduled, dated when it goes and naming its Bcc, and when it
// goes it is filed in Sent; what the recipients get has no Bcc.
func TestAHeldMessageShowsInScheduledAndIsFiledInSent(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	body := "From: inbox@example.org\r\nTo: someone@example.com\r\nSubject: Later\r\n\r\nHello.\r\n"
	if _, err := r.submitHeld("inbox@example.org", "correct horse", "inbox@example.org", "HOLDFOR=3600",
		[]string{"someone@example.com", "hidden@example.com"}, body); err != nil {
		t.Fatal(err)
	}
	shown, at := r.mailbox("inbox@example.org", "Scheduled")
	if len(shown) != 1 || !strings.HasPrefix(shown[0], "Bcc: hidden@example.com\r\n") || time.Until(at[0]) < 59*time.Minute {
		t.Fatalf("Scheduled shows %d, dated %v:\n%v", len(shown), at, shown)
	}

	r.st.SetClock(func() time.Time { return time.Now().Add(time.Hour + time.Minute) })
	r.sender.runOnce(ctx)
	if len(r.relay.got) != 1 || strings.Contains(string(r.relay.got[0].data), "hidden@example.com") {
		t.Fatalf("the relay got %+v", r.relay.got)
	}
	if shown, _ := r.mailbox("inbox@example.org", "Scheduled"); len(shown) != 0 {
		t.Errorf("Scheduled still shows %d", len(shown))
	}
	sent, _ := r.mailbox("inbox@example.org", "Sent")
	if len(sent) != 1 || !strings.Contains(sent[0], "Bcc: hidden@example.com") {
		t.Errorf("Sent has %v", sent)
	}
}

// Deleting a held message's copy in Scheduled cancels it, and so does the admin interface, for
// held mail only.
func TestDeletingAHeldMessageInScheduledCancelsIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	inbox, _ := r.st.Authenticate(ctx, "inbox@example.org", "correct horse")
	scheduled, _ := r.st.SpecialMailbox(ctx, inbox.ID, store.Scheduled)
	for range 2 {
		if _, err := r.submitHeld("inbox@example.org", "correct horse", "inbox@example.org", "HOLDFOR=3600",
			[]string{"someone@example.com"}, letter); err != nil {
			t.Fatal(err)
		}
	}
	uids, _ := r.st.UIDs(ctx, scheduled.ID)
	if len(uids) != 2 {
		t.Fatalf("Scheduled holds %d", len(uids))
	}
	if err := r.st.Expunge(ctx, scheduled.ID, uids[:1]); err != nil {
		t.Fatal(err)
	}
	q, _ := r.st.Outbound(ctx)
	if len(q) != 1 {
		t.Fatalf("the queue holds %d", len(q))
	}
	if err := r.st.CancelHeld(ctx, q[0].MessageID); err != nil {
		t.Fatal(err)
	}
	if q, _ := r.st.Outbound(ctx); len(q) != 0 {
		t.Errorf("the queue holds %d", len(q))
	}
	if uids, _ := r.st.UIDs(ctx, scheduled.ID); len(uids) != 0 {
		t.Errorf("Scheduled holds %d", len(uids))
	}
	r.st.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	r.sender.runOnce(ctx)
	if len(r.relay.got) != 0 {
		t.Errorf("cancelled mail went: %d", len(r.relay.got))
	}

	// Mail being sent already is not held, and stays.
	if err := r.submit("inbox@example.org", "correct horse", "inbox@example.org", []string{"someone@example.com"}, letter); err != nil {
		t.Fatal(err)
	}
	q, _ = r.st.Outbound(ctx)
	if err := r.st.CancelHeld(ctx, q[0].MessageID); !errors.Is(err, store.ErrConflict) {
		t.Errorf("cancelling mail being sent: %v", err)
	}
}
