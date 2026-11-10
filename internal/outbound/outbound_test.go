package outbound

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net"
	"net/smtp"
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

	d, err := st.CreateDomain(ctx, "reey.ru")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"inbox@reey.ru", "other@reey.ru"} {
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

const letter = "From: Misha <inbox@reey.ru>\r\nTo: someone@example.com\r\nSubject: Hi\r\n\r\nHello.\r\n"

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
	if err := r.submit("inbox@reey.ru", "correct horse", "inbox+news@reey.ru",
		[]string{"someone@example.com", "other@reey.ru"}, letter); err != nil {
		t.Fatal(err)
	}
	r.sender.runOnce(context.Background())

	if len(r.relay.got) != 1 {
		t.Fatalf("the relay got %d messages", len(r.relay.got))
	}
	got := r.relay.got[0]
	if got.from != "inbox+news@reey.ru" || len(got.rcpts) != 1 || got.rcpts[0] != "someone@example.com" {
		t.Errorf("the relay got %s → %v", got.from, got.rcpts)
	}
	// The signature verifies against the record mailio publishes.
	record, _ := dkimkey.Record(r.domain.DKIMKey)
	vs, err := dkim.VerifyWithOptions(bytes.NewReader(got.data), &dkim.VerifyOptions{
		LookupTXT: func(name string) ([]string, error) {
			if name != dkimkey.RecordName("reey.ru", dkimkey.Selector) {
				t.Errorf("looked up %s", name)
			}
			return []string{record}, nil
		},
	})
	if err != nil || len(vs) != 1 || vs[0].Err != nil || vs[0].Domain != "reey.ru" {
		t.Errorf("dkim: %v %+v", err, vs)
	}
	if !strings.Contains(string(got.data), "Message-ID: <") || !strings.Contains(string(got.data), "Date: ") {
		t.Error("Message-ID or Date was not added")
	}

	if local := r.inbox("other@reey.ru"); len(local) != 1 || !strings.Contains(local[0], "Subject: Hi") {
		t.Errorf("the local recipient got %d", len(local))
	}
	if q, _ := r.st.Outbound(context.Background()); len(q) != 0 {
		t.Errorf("the queue still holds %d", len(q))
	}
}

func TestAnAccountSendsOnlyAsItself(t *testing.T) {
	r := newRig(t)
	err := r.submit("inbox@reey.ru", "correct horse", "other@reey.ru", []string{"someone@example.com"}, letter)
	if err == nil || !strings.Contains(err.Error(), "553") {
		t.Errorf("sending as somebody else: %v", err)
	}
	err = r.submit("inbox@reey.ru", "wrong guess", "inbox@reey.ru", []string{"someone@example.com"}, letter)
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
	if err := r.submit("inbox@reey.ru", "correct horse", "inbox@reey.ru",
		[]string{"bad@example.com", "later@example.com"}, letter); err != nil {
		t.Fatal(err)
	}
	r.sender.runOnce(context.Background())

	bounces := r.inbox("inbox@reey.ru")
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
	if err := r.submit("inbox@reey.ru", "correct horse", "inbox@reey.ru", []string{"someone@example.com"}, letter); err != nil {
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

	if err := r.submit("inbox@reey.ru", "correct horse", "inbox@reey.ru", []string{"someone@example.com"}, letter); err != nil {
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
	inbox, _ := r.st.Authenticate(ctx, "inbox@reey.ru", "correct horse")
	other, _ := r.st.Authenticate(ctx, "other@reey.ru", "correct horse")

	if err := r.st.SetAccountMessageLimit(ctx, other.ID, 1<<20); err != nil {
		t.Fatal(err)
	}
	err := r.submit("inbox@reey.ru", "correct horse", "inbox@reey.ru", []string{"someone@example.com", "other@reey.ru"}, big)
	if err == nil || !strings.Contains(err.Error(), "552") || !strings.Contains(err.Error(), "other@reey.ru") {
		t.Errorf("a local recipient's: %v", err)
	}
	if err := r.submit("inbox@reey.ru", "correct horse", "inbox@reey.ru", []string{"someone@example.com"}, big); err != nil {
		t.Errorf("within the sender's: %v", err)
	}

	if err := r.st.SetAccountMessageLimit(ctx, inbox.ID, 1<<20); err != nil {
		t.Fatal(err)
	}
	err = r.submit("inbox@reey.ru", "correct horse", "inbox@reey.ru", []string{"someone@example.com"}, big)
	if err == nil || !strings.Contains(err.Error(), "552") || !strings.Contains(err.Error(), "inbox@reey.ru sends up to 1 MB") {
		t.Errorf("the sender's own: %v", err)
	}
}
