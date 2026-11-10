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
	s.MaxMessageBytes = store.MaxMessageBytes
	s.MaxRecipients = MaxRecipients
	s.ReadTimeout = 5 * time.Minute
	s.WriteTimeout = time.Minute
	s.EnableSMTPUTF8 = true
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
}

func (s *subSession) Logout() error { return nil }

func (s *subSession) Mail(from string, _ *smtp.MailOptions) error {
	if s.account == nil {
		return smtp.ErrAuthRequired
	}
	a, err := address.Parse(from)
	// An account sends as itself, tagged or not: from inbox+shop@ as readily as inbox@.
	if err != nil || a.Domain != s.account.Domain || a.Name() != s.account.Local {
		return &smtp.SMTPError{Code: 553, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "You can only send as " + s.account.Address()}
	}
	s.Reset()
	s.from = a.String()
	return nil
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
	ctx := context.Background()
	raw, err := s.sub.prepare(ctx, s.account, NormalizeCRLF(body))
	if err != nil {
		s.sub.log.Error("preparing a message failed", "account", s.account.ID, "error", err)
		return errTemporary
	}

	// Mail between accounts of this server never leaves it.
	var local []store.Delivery
	var remote []string
	seen := map[int64]bool{}
	for _, rcpt := range s.rcpts {
		a, _ := address.Parse(rcpt)
		res, err := s.sub.st.Resolve(ctx, a)
		if err != nil {
			return errTemporary
		}
		if !res.Ours {
			remote = append(remote, rcpt)
			continue
		}
		if res.Account == nil || res.Drop {
			continue
		}
		box, err := s.sub.st.MailboxNamed(ctx, res.Account.ID, store.Inbox)
		if err != nil {
			return errTemporary
		}
		if res.Junk {
			if j, err := s.sub.st.SpecialMailbox(ctx, res.Account.ID, `\Junk`); err == nil {
				box = j
			}
		}
		if !seen[box.ID] {
			seen[box.ID] = true
			local = append(local, store.Delivery{MailboxID: box.ID})
		}
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

// prepare adds what a client may leave out, stamps Received, and signs with the domain's DKIM key.
func (sub *Submission) prepare(ctx context.Context, u *store.Account, body []byte) ([]byte, error) {
	var head bytes.Buffer
	fmt.Fprintf(&head, "Received: by %s (mailio) with ESMTPSA;\r\n\t%s\r\n", sub.host, time.Now().Format(time.RFC1123Z))
	if m, err := mail.ReadMessage(bytes.NewReader(body)); err == nil {
		if m.Header.Get("Date") == "" {
			fmt.Fprintf(&head, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
		}
		if m.Header.Get("Message-ID") == "" {
			id := make([]byte, 12)
			rand.Read(id)
			fmt.Fprintf(&head, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), u.Domain)
		}
	}
	raw := append(head.Bytes(), body...)

	d, err := sub.st.DomainByID(ctx, u.DomainID)
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
