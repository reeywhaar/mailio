package smtpd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"mailio/internal/address"
	"mailio/internal/app"
	"mailio/internal/dkimkey"
	"mailio/internal/store"
)

// MaxHold is the longest a client may have mailio hold a message before sending it
// (FUTURERELEASE, RFC 4865).
const MaxHold = 365 * 24 * time.Hour

// Queue is where submitted mail for other servers goes.
type Queue interface {
	Wake()
}

// Submission accepts mail from mailio's own accounts, signed in, to send anywhere.
type Submission struct {
	st    *store.Store
	log   *slog.Logger
	host  string
	queue Queue
}

// NewSubmission makes the backend for 465 and 587.
func NewSubmission(st *store.Store, log *slog.Logger, host string, queue Queue) *Submission {
	return &Submission{st: st, log: log, host: host, queue: queue}
}

// Server is the SMTP server for submission. AUTH is offered only over TLS, which on 587 means
// after STARTTLS.
func (sub *Submission) Server(cfg *tls.Config) *smtp.Server {
	s := smtp.NewServer(sub)
	s.Domain = sub.host
	s.TLSConfig = cfg
	// The most any account can send; its own limit is checked at MAIL and after DATA.
	s.MaxMessageBytes = store.MessageCeiling
	s.MaxRecipients = MaxRecipients
	s.ReadTimeout = 5 * time.Minute
	s.WriteTimeout = time.Minute
	s.EnableSMTPUTF8 = true
	// A client may ask for a message to be sent later, with HOLDFOR or HOLDUNTIL on MAIL.
	s.EnableFUTURERELEASE = true
	s.MaxFutureRelease = MaxHold
	s.ErrorLog = logger{sub.log}
	return s
}

// NewSession is one connection.
func (sub *Submission) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &subSession{sub: sub, ip: peerIP(c.Conn())}, nil
}

type subSession struct {
	sub     *Submission
	ip      net.IP
	account *store.Account
	from    string
	rcpts   []string
	// size is what the client said the message is, or 0. limit is the smallest of the account's
	// own and those of the local recipients, and limitFor that recipient, or "" for the account.
	size     int64
	limit    int64
	limitFor string
	// hold is when the client asked the message to be sent, or zero for at once.
	hold time.Time
}

var _ smtp.AuthSession = (*subSession)(nil)

func (s *subSession) AuthMechanisms() []string { return []string{sasl.Plain, sasl.Login} }

func (s *subSession) Auth(mech string) (sasl.Server, error) {
	check := func(username, password string) error {
		u, err := s.sub.st.Authenticate(context.Background(), username, password)
		if errors.Is(err, store.ErrNotFound) {
			s.sub.log.Warn(app.PasswordRefused, "ip", s.ip.String(), "via", "submission")
			time.Sleep(time.Second)
			return smtp.ErrAuthFailed
		}
		if err != nil {
			return err
		}
		s.account = u
		return nil
	}
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			if identity != "" && identity != username {
				return smtp.ErrAuthFailed
			}
			return check(username, password)
		}), nil
	case sasl.Login:
		return &loginServer{check: check}, nil
	}
	return nil, smtp.ErrAuthUnknownMechanism
}

func (s *subSession) Reset() {
	s.from = ""
	s.rcpts = nil
	s.size, s.limit, s.limitFor = 0, 0, ""
	s.hold = time.Time{}
}

func (s *subSession) Logout() error { return nil }

func (s *subSession) Mail(from string, opts *smtp.MailOptions) error {
	if s.account == nil {
		return smtp.ErrAuthRequired
	}
	a, err := address.Parse(from)
	// An account sends as itself and as its aliases, tagged or not: from inbox+shop@ as readily
	// as inbox@.
	if err != nil || !s.sendsAs(a) {
		return &smtp.SMTPError{Code: 553, EnhancedCode: smtp.EnhancedCode{5, 7, 1},
			Message: "You can only send as " + s.account.Address() + " or an alias of it"}
	}
	s.Reset()
	s.from = a.String()
	s.limit = s.account.MessageLimit
	if opts != nil {
		s.size = opts.Size
		// A time already past is now.
		if opts.HoldUntil.After(time.Now()) {
			s.hold = opts.HoldUntil
		}
	}
	if s.size > s.limit {
		return s.tooBig()
	}
	return nil
}

// sendsAs is whether the account may send from a: its own address or one of its aliases.
func (s *subSession) sendsAs(a address.Address) bool {
	if a.Domain == s.account.Domain && a.Name() == s.account.Local {
		return true
	}
	_, err := s.sub.st.AliasOf(context.Background(), s.account.ID, a)
	return err == nil
}

// tooBig refuses a message larger than the account sends, or than a local recipient takes.
func (s *subSession) tooBig() error {
	if s.limitFor != "" {
		return tooBig(s.limitFor, s.limit)
	}
	return &smtp.SMTPError{Code: 552, EnhancedCode: smtp.EnhancedCode{5, 2, 3},
		Message: fmt.Sprintf("Message too big: %s sends up to %s", s.account.Address(), store.Megabytes(s.limit))}
}

func (s *subSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	if s.account == nil {
		return smtp.ErrAuthRequired
	}
	a, err := address.Parse(to)
	if err != nil {
		return &smtp.SMTPError{Code: 501, EnhancedCode: smtp.EnhancedCode{5, 1, 3}, Message: "That is not an address"}
	}
	// A local address that takes no mail is refused now, while the sender's client can say so.
	res, err := s.sub.st.Resolve(context.Background(), a)
	if err != nil {
		return errTemporary
	}
	if res.Refused {
		return errNoMail
	}
	if res.Account != nil && res.Limit < s.limit {
		if s.size > res.Limit {
			return tooBig(a.String(), res.Limit)
		}
		s.limit, s.limitFor = res.Limit, a.String()
	}
	s.rcpts = append(s.rcpts, a.String())
	return nil
}

func (s *subSession) Data(r io.Reader) error {
	if s.account == nil {
		return smtp.ErrAuthRequired
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if int64(len(body)) > s.limit {
		s.sub.log.Info("mail refused: larger than allowed", "account", s.account.ID, "bytes", len(body), "limit", s.limit)
		return s.tooBig()
	}
	ctx := context.Background()
	raw, err := s.sub.prepare(ctx, s.from, NormalizeCRLF(body))
	if err != nil {
		s.sub.log.Error("preparing a message failed", "account", s.account.ID, "error", err)
		return errTemporary
	}

	// Held, every recipient waits in the queue, those here too: the time is the sender's.
	if !s.hold.IsZero() {
		held := store.Held{Sender: s.from, AccountID: s.account.ID, Raw: raw, Recipients: s.rcpts, Release: s.hold,
			Copy: scheduledCopy(raw, s.rcpts)}
		if err := s.sub.st.Hold(ctx, held); err != nil {
			s.sub.log.Error("holding a message failed", "account", s.account.ID, "error", err)
			return errTemporary
		}
		s.sub.queue.Wake()
		s.sub.log.Info("mail held", "account", s.account.ID, "recipients", len(s.rcpts), "until", s.hold.UTC().Format(time.RFC3339), "bytes", len(raw))
		return nil
	}

	// Mail between accounts of this server never leaves it.
	local, remote, err := s.sub.st.Deliveries(ctx, s.rcpts)
	if err != nil {
		s.sub.log.Error("resolving recipients failed", "account", s.account.ID, "error", err)
		return errTemporary
	}
	if len(local) > 0 {
		if err := s.sub.st.Deliver(ctx, raw, time.Now(), local); err != nil {
			return errTemporary
		}
	}
	if len(remote) > 0 {
		if err := s.sub.st.Enqueue(ctx, s.from, s.account.ID, raw, remote); err != nil {
			return errTemporary
		}
		s.sub.queue.Wake()
	}
	s.sub.log.Info("mail submitted", "account", s.account.ID, "local", len(local), "remote", len(remote), "bytes", len(raw))
	return nil
}

// scheduledCopy is what a held message's sender sees in Scheduled until it goes, and in Sent after:
// the message, with a Bcc naming the recipients its headers do not, whom only the envelope knew, so
// a client that edits it sends to them again.
func scheduledCopy(raw []byte, rcpts []string) []byte {
	named := map[string]bool{}
	if m, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
		for _, h := range []string{"To", "Cc", "Bcc"} {
			list, _ := m.Header.AddressList(h)
			for _, a := range list {
				named[strings.ToLower(a.Address)] = true
			}
		}
	}
	var bcc []string
	for _, r := range rcpts {
		if !named[strings.ToLower(r)] {
			bcc = append(bcc, r)
		}
	}
	if len(bcc) == 0 {
		return raw
	}
	return append([]byte("Bcc: "+strings.Join(bcc, ", ")+"\r\n"), raw...)
}

// prepare adds what a client may leave out, stamps Received, and signs with the DKIM key of the
// domain it is sent from, an alias's when it is sent as one.
func (sub *Submission) prepare(ctx context.Context, from string, body []byte) ([]byte, error) {
	sender, err := address.Parse(from)
	if err != nil {
		return nil, err
	}
	var head bytes.Buffer
	fmt.Fprintf(&head, "Received: by %s (mailio) with ESMTPSA;\r\n\t%s\r\n", sub.host, time.Now().Format(time.RFC1123Z))
	if m, err := mail.ReadMessage(bytes.NewReader(body)); err == nil {
		if m.Header.Get("Date") == "" {
			fmt.Fprintf(&head, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
		}
		if m.Header.Get("Message-ID") == "" {
			id := make([]byte, 12)
			rand.Read(id)
			fmt.Fprintf(&head, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), sender.Domain)
		}
	}
	raw := append(head.Bytes(), body...)

	d, err := sub.st.DomainByName(ctx, sender.Domain)
	if err != nil {
		return nil, err
	}
	if d.DKIMKey == "" {
		return raw, nil
	}
	var signed bytes.Buffer
	if err := dkimkey.Sign(&signed, bytes.NewReader(raw), d.Name, d.DKIMSelector, d.DKIMKey); err != nil {
		return nil, err
	}
	return signed.Bytes(), nil
}

// loginServer is SASL LOGIN, which go-sasl only has a client for. Obsolete, and still what some
// clients, Outlook among them, prefer.
type loginServer struct {
	check    func(username, password string) error
	username string
	step     int
}

func (l *loginServer) Next(response []byte) ([]byte, bool, error) {
	switch l.step {
	case 0:
		l.step++
		if len(response) > 0 {
			l.username = string(response)
			l.step++
			return []byte("Password:"), false, nil
		}
		return []byte("Username:"), false, nil
	case 1:
		l.username = string(response)
		l.step++
		return []byte("Password:"), false, nil
	case 2:
		return nil, true, l.check(strings.TrimSpace(l.username), string(response))
	}
	return nil, true, smtp.ErrAuthFailed
}
