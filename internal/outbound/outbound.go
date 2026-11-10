// Package outbound delivers queued mail: through the relay when one is set, else to each
// recipient's MX on port 25. A temporary failure is retried for five days; a permanent one, or
// the five days running out, is reported to the sender in their INBOX.
package outbound

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"net/textproto"
	"sort"
	"strings"
	"time"

	"mailio/internal/store"
)

// GiveUpAfter is how long a recipient is retried before the sender is told it failed. RFC 5321
// says four to five days.
const GiveUpAfter = 5 * 24 * time.Hour

// backoff is the wait after each failed attempt; the last repeats.
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour}

// Sender runs the queue.
type Sender struct {
	st   *store.Store
	log  *slog.Logger
	host string
	wake chan struct{}
	// dial is net.Dialer.DialContext, replaced in tests.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// mx is net.LookupMX, replaced in tests.
	mx func(ctx context.Context, domain string) ([]*net.MX, error)
	// roots verifies the relay; nil is the system's.
	roots *x509.CertPool
}

// New makes the queue runner. host is the name it greets other servers with.
func New(st *store.Store, log *slog.Logger, host string) *Sender {
	d := &net.Dialer{Timeout: 30 * time.Second}
	return &Sender{st: st, log: log, host: host, wake: make(chan struct{}, 1),
		dial: d.DialContext, mx: net.DefaultResolver.LookupMX}
}

// Wake runs the queue now rather than at the next tick: something was just queued.
func (s *Sender) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run delivers until ctx ends.
func (s *Sender) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		s.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

func (s *Sender) runOnce(ctx context.Context) {
	due, err := s.st.DueOutbound(ctx, 500)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Error("reading the outbound queue failed", "error", err)
		}
		return
	}
	// Held mail to accounts here was queued too, and is delivered here now its time has come.
	due = s.deliverLocal(ctx, due)
	// The relay is the sending account's own, its domain's, or the default; a message has one
	// sender, so one relay, and nil sends it straight to each recipient's MX.
	relays := map[string]*store.Relay{}
	relayFor := func(r store.OutboundRecipient) (*store.Relay, error) {
		key := r.AccountID + " " + domainOf(r.Sender)
		if relay, ok := relays[key]; ok {
			return relay, nil
		}
		relay, err := s.st.RelayFor(ctx, r.AccountID, domainOf(r.Sender))
		if err == nil {
			relays[key] = relay
		}
		return relay, err
	}
	// One SMTP transaction per message per destination: the relay takes every recipient at once,
	// and an MX takes those at its own domain.
	type batch struct {
		messageID int64
		relay     *store.Relay
		dest      string
		rcpts     []store.OutboundRecipient
	}
	var batches []*batch
	index := map[string]*batch{}
	for _, r := range due {
		relay, err := relayFor(r)
		if err != nil {
			s.log.Error("reading the relay failed", "error", err)
			return
		}
		dest := "relay"
		if relay == nil {
			dest = domainOf(r.Recipient)
		}
		key := fmt.Sprintf("%d %s", r.MessageID, dest)
		b := index[key]
		if b == nil {
			b = &batch{messageID: r.MessageID, relay: relay, dest: dest}
			index[key] = b
			batches = append(batches, b)
		}
		b.rcpts = append(b.rcpts, r)
	}
	for _, b := range batches {
		if ctx.Err() != nil {
			return
		}
		raw, err := s.st.OutboundRaw(ctx, b.messageID)
		if err != nil {
			s.log.Error("reading a queued message failed", "error", err)
			continue
		}
		var results map[int64]error
		if b.relay != nil {
			results = s.viaRelay(ctx, b.relay, b.rcpts, raw)
		} else {
			results = s.direct(ctx, b.dest, b.rcpts, raw)
		}
		s.settle(ctx, b.rcpts, raw, results)
	}
}

// deliverLocal delivers the due recipients at mailio's own domains to their mailboxes, and returns
// the rest, for other servers. Only held mail queues a recipient here.
func (s *Sender) deliverLocal(ctx context.Context, due []store.OutboundRecipient) []store.OutboundRecipient {
	byMessage := map[int64][]store.OutboundRecipient{}
	var messages []int64
	var remote []store.OutboundRecipient
	for _, r := range due {
		if _, err := s.st.DomainByName(ctx, domainOf(r.Recipient)); err != nil {
			remote = append(remote, r)
			continue
		}
		if byMessage[r.MessageID] == nil {
			messages = append(messages, r.MessageID)
		}
		byMessage[r.MessageID] = append(byMessage[r.MessageID], r)
	}
	for _, id := range messages {
		rcpts := byMessage[id]
		err := s.deliverHere(ctx, id, rcpts)
		if err != nil {
			s.log.Error("delivering held mail here failed", "error", err)
			for _, r := range rcpts {
				wait := backoff[min(r.Attempts, len(backoff)-1)]
				if err := s.st.DeferOutbound(ctx, r.ID, time.Now().Add(wait), err.Error()); err != nil {
					s.log.Error("deferring a recipient failed", "error", err)
				}
			}
			continue
		}
		done := make([]int64, len(rcpts))
		for i, r := range rcpts {
			done[i] = r.ID
		}
		if err := s.st.FinishOutbound(ctx, done); err != nil {
			s.log.Error("finishing recipients failed", "error", err)
		}
		s.log.Info("held mail delivered here", "account", rcpts[0].AccountID, "recipients", len(rcpts))
	}
	return remote
}

func (s *Sender) deliverHere(ctx context.Context, messageID int64, rcpts []store.OutboundRecipient) error {
	raw, err := s.st.OutboundRaw(ctx, messageID)
	if err != nil {
		return err
	}
	addrs := make([]string, len(rcpts))
	for i, r := range rcpts {
		addrs[i] = r.Recipient
	}
	to, _, err := s.st.Deliveries(ctx, addrs)
	if err != nil || len(to) == 0 {
		return err
	}
	return s.st.Deliver(ctx, raw, time.Now(), to)
}

// settle records what happened to each recipient.
func (s *Sender) settle(ctx context.Context, rcpts []store.OutboundRecipient, raw []byte, results map[int64]error) {
	var done []int64
	var failed []store.OutboundRecipient
	var reasons []string
	for _, r := range rcpts {
		err := results[r.ID]
		switch {
		case err == nil:
			done = append(done, r.ID)
			s.log.Info("mail sent", "account", r.AccountID, "attempts", r.Attempts+1)
		case permanent(err) || time.Since(r.Since()) > GiveUpAfter:
			done = append(done, r.ID)
			failed = append(failed, r)
			reasons = append(reasons, err.Error())
			s.log.Warn("mail could not be delivered", "account", r.AccountID, "attempts", r.Attempts+1, "error", err)
		default:
			wait := backoff[min(r.Attempts, len(backoff)-1)]
			if err := s.st.DeferOutbound(ctx, r.ID, time.Now().Add(wait), err.Error()); err != nil {
				s.log.Error("deferring a recipient failed", "error", err)
			}
			s.log.Info("mail deferred", "account", r.AccountID, "attempts", r.Attempts+1, "retry_in", wait, "error", err)
		}
	}
	if err := s.st.FinishOutbound(ctx, done); err != nil {
		s.log.Error("finishing recipients failed", "error", err)
	}
	for i, r := range failed {
		if err := s.bounce(ctx, r, raw, reasons[i]); err != nil {
			s.log.Error("reporting a failure to its sender failed", "account", r.AccountID, "error", err)
		}
	}
}

// permanent is a 5xx about the message: retrying gets the same answer. A refusal while setting up
// the relay is not one, however it is coded: a wrong relay password is fixed in the admin
// interface, and the mail should be waiting when it is.
func permanent(err error) bool {
	var setup *setupError
	if errors.As(err, &setup) {
		return false
	}
	var te *textproto.Error
	return errors.As(err, &te) && te.Code >= 500
}

type setupError struct{ err error }

func (e *setupError) Error() string { return "relay: " + e.err.Error() }
func (e *setupError) Unwrap() error { return e.err }

func setup(err error) error { return &setupError{err} }

// refusal is a permanent failure mailio decides on itself.
func refusal(code int, msg string) error { return &textproto.Error{Code: code, Msg: msg} }

func (s *Sender) viaRelay(ctx context.Context, relay *store.Relay, rcpts []store.OutboundRecipient, raw []byte) map[int64]error {
	addr := net.JoinHostPort(relay.Host, fmt.Sprint(relay.Port))
	conn, err := s.dial(ctx, "tcp", addr)
	if err != nil {
		return all(rcpts, setup(err))
	}
	cfg := &tls.Config{ServerName: relay.Host, MinVersion: tls.VersionTLS12, RootCAs: s.roots}
	if relay.Security == store.SecurityImplicit {
		conn = tls.Client(conn, cfg)
	}
	c, err := smtp.NewClient(conn, relay.Host)
	if err != nil {
		conn.Close()
		return all(rcpts, setup(err))
	}
	defer c.Close()
	if err := c.Hello(s.host); err != nil {
		return all(rcpts, setup(err))
	}
	if relay.Security == store.SecurityStartTLS {
		if err := c.StartTLS(cfg); err != nil {
			return all(rcpts, setup(err))
		}
	}
	if relay.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", relay.Username, relay.Password, relay.Host)); err != nil {
			return all(rcpts, setup(err))
		}
	}
	return transact(c, rcpts, raw)
}

// TestRelay connects to a relay, encrypts, and signs in, sending nothing: what an admin checks
// after typing its details.
func (s *Sender) TestRelay(ctx context.Context, relay *store.Relay) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := s.dial(ctx, "tcp", net.JoinHostPort(relay.Host, fmt.Sprint(relay.Port)))
	if err != nil {
		return err
	}
	cfg := &tls.Config{ServerName: relay.Host, MinVersion: tls.VersionTLS12, RootCAs: s.roots}
	if relay.Security == store.SecurityImplicit {
		conn = tls.Client(conn, cfg)
	}
	c, err := smtp.NewClient(conn, relay.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err := c.Hello(s.host); err != nil {
		return err
	}
	if relay.Security == store.SecurityStartTLS {
		if err := c.StartTLS(cfg); err != nil {
			return err
		}
	}
	if relay.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", relay.Username, relay.Password, relay.Host)); err != nil {
			return err
		}
	}
	return c.Quit()
}

// direct delivers to a domain's mail servers in preference order, falling back to its address
// when it publishes no MX.
func (s *Sender) direct(ctx context.Context, domain string, rcpts []store.OutboundRecipient, raw []byte) map[int64]error {
	hosts, err := s.mxHosts(ctx, domain)
	if err != nil {
		return all(rcpts, err)
	}
	var last map[int64]error
	for _, host := range hosts {
		conn, err := s.dial(ctx, "tcp", net.JoinHostPort(host, "25"))
		if err != nil {
			last = all(rcpts, err)
			continue
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			conn.Close()
			last = all(rcpts, err)
			continue
		}
		results := func() map[int64]error {
			defer c.Close()
			if err := c.Hello(s.host); err != nil {
				return all(rcpts, err)
			}
			// Opportunistic: encrypted whenever offered. Unverified, as every MTA does without
			// MTA-STS, because refusing a self-signed MX would be refusing to deliver.
			if ok, _ := c.Extension("STARTTLS"); ok {
				if err := c.StartTLS(&tls.Config{ServerName: host, InsecureSkipVerify: true}); err != nil {
					return all(rcpts, err)
				}
			}
			return transact(c, rcpts, raw)
		}()
		if !anyTemporary(results) {
			return results
		}
		last = results
	}
	return last
}

func (s *Sender) mxHosts(ctx context.Context, domain string) ([]string, error) {
	records, err := s.mx(ctx, domain)
	var dnsErr *net.DNSError
	if err != nil && !(errors.As(err, &dnsErr) && dnsErr.IsNotFound) {
		return nil, err
	}
	if len(records) == 1 && records[0].Host == "." {
		return nil, refusal(556, domain+" accepts no mail")
	}
	if len(records) == 0 {
		if err != nil {
			return nil, refusal(550, "no such domain: "+domain)
		}
		return []string{domain}, nil
	}
	sort.SliceStable(records, func(i, j int) bool { return records[i].Pref < records[j].Pref })
	out := make([]string, len(records))
	for i, r := range records {
		out[i] = strings.TrimSuffix(r.Host, ".")
	}
	return out, nil
}

// transact sends one message to every recipient the server accepts.
func transact(c *smtp.Client, rcpts []store.OutboundRecipient, raw []byte) map[int64]error {
	results := map[int64]error{}
	if err := c.Mail(rcpts[0].Sender); err != nil {
		return all(rcpts, err)
	}
	var accepted []store.OutboundRecipient
	for _, r := range rcpts {
		if err := c.Rcpt(r.Recipient); err != nil {
			results[r.ID] = err
			continue
		}
		accepted = append(accepted, r)
	}
	if len(accepted) == 0 {
		return results
	}
	w, err := c.Data()
	if err != nil {
		return merge(results, all(accepted, err))
	}
	if _, err := w.Write(raw); err != nil {
		return merge(results, all(accepted, err))
	}
	if err := w.Close(); err != nil {
		return merge(results, all(accepted, err))
	}
	for _, r := range accepted {
		results[r.ID] = nil
	}
	c.Quit()
	return results
}

func all(rcpts []store.OutboundRecipient, err error) map[int64]error {
	out := make(map[int64]error, len(rcpts))
	for _, r := range rcpts {
		out[r.ID] = err
	}
	return out
}

func merge(a, b map[int64]error) map[int64]error {
	for k, v := range b {
		a[k] = v
	}
	return a
}

func anyTemporary(results map[int64]error) bool {
	for _, err := range results {
		if err != nil && !permanent(err) {
			return true
		}
	}
	return false
}

func domainOf(addr string) string {
	if i := strings.LastIndexByte(addr, '@'); i >= 0 {
		return strings.ToLower(addr[i+1:])
	}
	return addr
}

// bounce tells the sender, in their INBOX, that a recipient never got it. Delivered locally: a
// report about failed delivery that could itself fail to deliver helps nobody.
func (s *Sender) bounce(ctx context.Context, r store.OutboundRecipient, raw []byte, reason string) error {
	if r.AccountID == "" {
		return nil
	}
	box, err := s.st.MailboxNamed(ctx, r.AccountID, store.Inbox)
	if err != nil {
		return err
	}
	headers := raw
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		headers = raw[:i+2]
	}
	var b bytes.Buffer
	now := time.Now()
	fmt.Fprintf(&b, "From: Mail Delivery <mailer-daemon@%s>\r\n", s.host)
	fmt.Fprintf(&b, "To: <%s>\r\n", r.Sender)
	fmt.Fprintf(&b, "Subject: Undelivered mail to %s\r\n", r.Recipient)
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%d.%d@%s>\r\n", now.UnixNano(), r.ID, s.host)
	b.WriteString("Auto-Submitted: auto-replied\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n")
	fmt.Fprintf(&b, "Your message to %s could not be delivered.\r\n\r\n", r.Recipient)
	fmt.Fprintf(&b, "The last answer was:\r\n\r\n    %s\r\n\r\n", strings.ReplaceAll(reason, "\n", "\r\n    "))
	b.WriteString("Its headers follow.\r\n\r\n")
	b.Write(headers)
	return s.st.Deliver(ctx, b.Bytes(), now, []store.Delivery{{MailboxID: box.ID}})
}
