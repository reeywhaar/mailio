// Package smtpd speaks SMTP: inbound on 25, where other servers deliver to mailio's accounts, and
// submission on 465 and 587, where those accounts send.
package smtpd

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/emersion/go-smtp"

	"mailio/internal/address"
	"mailio/internal/store"
)

// MaxRecipients is per message. A real message to more than this many of one server's accounts is
// a mailing list, and lists split their sends.
const MaxRecipients = 100

// Inbound accepts mail for mailio's accounts from the rest of the internet.
type Inbound struct {
	st     *store.Store
	log    *slog.Logger
	host   string
	checks Checker
}

// NewInbound makes the port-25 backend. host is the name mailio greets with and stamps on
// Received and Authentication-Results.
func NewInbound(st *store.Store, log *slog.Logger, host string, checks Checker) *Inbound {
	return &Inbound{st: st, log: log, host: host, checks: checks}
}

// Server is the SMTP server for port 25: STARTTLS offered, no AUTH, mail only for our domains.
func (in *Inbound) Server(cfg *tls.Config) *smtp.Server {
	s := smtp.NewServer(in)
	s.Domain = in.host
	s.TLSConfig = cfg
	s.MaxMessageBytes = store.MaxMessageBytes
	s.MaxRecipients = MaxRecipients
	s.ReadTimeout = 5 * time.Minute
	s.WriteTimeout = time.Minute
	s.EnableSMTPUTF8 = true
	s.ErrorLog = logger{in.log}
	return s
}

// NewSession is one connection.
func (in *Inbound) NewSession(c *smtp.Conn) (smtp.Session, error) {
	return &inSession{in: in, conn: c, ip: peerIP(c.Conn())}, nil
}

type inSession struct {
	in    *Inbound
	conn  *smtp.Conn
	ip    net.IP
	from  string
	rcpts []*store.Recipient
}

func (s *inSession) Reset() {
	s.from = ""
	s.rcpts = nil
}

func (s *inSession) Logout() error { return nil }

func (s *inSession) Mail(from string, _ *smtp.MailOptions) error {
	s.Reset()
	s.from = from
	return nil
}

func (s *inSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	a, err := address.Parse(to)
	if err != nil {
		return &smtp.SMTPError{Code: 501, EnhancedCode: smtp.EnhancedCode{5, 1, 3}, Message: "That is not an address"}
	}
	r, err := s.in.st.Resolve(context.Background(), a)
	if err != nil {
		s.in.log.Error("resolve failed", "error", err)
		return errTemporary
	}
	switch {
	case !r.Ours:
		s.in.log.Info("mail refused: not a domain mailio receives for", "ip", s.ip.String())
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "Relaying denied"}
	case r.Refused:
		s.in.log.Info("mail refused: the address takes no mail", "ip", s.ip.String())
		return errNoMail
	case r.Account == nil && !r.Drop:
		s.in.log.Info("mail refused: no such account", "ip", s.ip.String())
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "No such user here"}
	}
	s.rcpts = append(s.rcpts, r)
	return nil
}

// errNoMail is an address that exists and takes no mail: one that only sends.
var errNoMail = &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 2, 1}, Message: "This address does not receive mail"}

var errTemporary = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Temporary failure, try again later"}

func (s *inSession) Data(r io.Reader) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	body = NormalizeCRLF(body)
	ctx := context.Background()

	_, secure := s.conn.TLSConnectionState()
	v := s.in.checks.Check(ctx, s.ip, s.conn.Hostname(), s.from, body)
	if v.Reject != "" {
		s.in.log.Info("mail refused by its sender's DMARC policy", "ip", s.ip.String())
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: v.Reject}
	}

	var head bytes.Buffer
	fmt.Fprintf(&head, "Return-Path: <%s>\r\n", s.from)
	if v.AuthResults != "" {
		fmt.Fprintf(&head, "Authentication-Results: %s\r\n", v.AuthResults)
	}
	with := "ESMTP"
	if secure {
		with = "ESMTPS"
	}
	fmt.Fprintf(&head, "Received: from %s (%s)\r\n\tby %s (mailio) with %s;\r\n\t%s\r\n",
		sanitize(s.conn.Hostname()), s.ip, s.in.host, with, time.Now().Format(time.RFC1123Z))
	raw := append(head.Bytes(), body...)

	deliveries, accounts, err := s.in.deliveries(ctx, s.rcpts, v.Junk)
	if err != nil {
		s.in.log.Error("delivery failed", "error", err)
		return errTemporary
	}
	if len(deliveries) > 0 {
		if err := s.in.st.Deliver(ctx, raw, time.Now(), deliveries); err != nil {
			s.in.log.Error("delivery failed", "error", err)
			return errTemporary
		}
	}
	s.in.log.Info("mail received", "accounts", accounts, "bytes", len(raw), "spf", v.SPF, "dkim", v.DKIM,
		"dmarc", v.DMARC, "junk", v.Junk, "tls", secure)
	return nil
}

// deliveries decides each recipient's mailbox: INBOX, or Junk for junk. One copy per mailbox,
// however many of an account's tagged addresses it was sent to.
func (in *Inbound) deliveries(ctx context.Context, rcpts []*store.Recipient, junk bool) ([]store.Delivery, []string, error) {
	seen := make(map[int64]bool)
	var (
		out      []store.Delivery
		accounts []string
	)
	for _, r := range rcpts {
		if r.Drop || r.Account == nil {
			continue
		}
		box, err := in.st.MailboxNamed(ctx, r.Account.ID, store.Inbox)
		if err != nil {
			return nil, nil, err
		}
		if r.Junk || junk {
			if j, err := in.st.SpecialMailbox(ctx, r.Account.ID, `\Junk`); err == nil {
				box = j
			} else if !errors.Is(err, store.ErrNotFound) {
				return nil, nil, err
			}
		}
		if seen[box.ID] {
			continue
		}
		seen[box.ID] = true
		out = append(out, store.Delivery{MailboxID: box.ID})
		accounts = append(accounts, r.Account.ID)
	}
	return out, accounts, nil
}

// NormalizeCRLF makes every line end in CRLF. A bare LF passed on is what SMTP smuggling rides
// on: the next server may read it as a line end that this one did not.
func NormalizeCRLF(b []byte) []byte {
	if bytes.IndexByte(b, '\n') < 0 && bytes.IndexByte(b, '\r') < 0 {
		return b
	}
	out := make([]byte, 0, len(b)+len(b)/50)
	for i := 0; i < len(b); i++ {
		switch c := b[i]; c {
		case '\r':
			out = append(out, '\r', '\n')
			if i+1 < len(b) && b[i+1] == '\n' {
				i++
			}
		case '\n':
			out = append(out, '\r', '\n')
		default:
			out = append(out, c)
		}
	}
	return out
}

// sanitize keeps a client-supplied name from breaking a header it is written into.
func sanitize(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c < 0x21 || c > 0x7e || c == '(' || c == ')' {
			b[i] = '_'
		}
	}
	if len(b) > 255 {
		b = b[:255]
	}
	return string(b)
}

func peerIP(c net.Conn) net.IP {
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP
	}
	host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
	return net.ParseIP(host)
}

type logger struct{ log *slog.Logger }

func (l logger) Printf(format string, args ...any) {
	l.log.Warn("smtp: " + fmt.Sprintf(format, args...))
}
func (l logger) Println(args ...any) { l.log.Warn("smtp: " + fmt.Sprint(args...)) }
