package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// How the connection to a relay is encrypted. There is no third option: a password crossing the
// network in the clear is not a choice somebody should make by accident.
const (
	SecurityStartTLS = "starttls"
	SecurityImplicit = "implicit"
)

// Relay is the server outbound mail is handed to, for a host whose provider blocks port 25.
type Relay struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string
}

func (s *Store) relayAAD() []byte { return []byte("relay-password") }

// Relay returns the relay, or nil when mailio delivers to recipients' servers itself.
func (s *Store) Relay(ctx context.Context) (*Relay, error) {
	var (
		r      Relay
		sealed []byte
	)
	err := s.reader.QueryRowContext(ctx,
		`SELECT host, port, security, username, password_sealed FROM relay WHERE id = 1`).
		Scan(&r.Host, &r.Port, &r.Security, &r.Username, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	if sealed != nil {
		if s.sealer == nil {
			return nil, errors.New("relay: the password is sealed and this store has no key")
		}
		plain, err := s.sealer.Open(sealed, s.relayAAD())
		if err != nil {
			return nil, Invalid("The relay's password was sealed with another MAILIO_SECRET_KEY. Enter it again.")
		}
		r.Password = string(plain)
	}
	return &r, nil
}

// SetRelay writes the relay. An empty password keeps the stored one for the same username, so a
// port can be corrected without retyping a credential the form was never shown.
func (s *Store) SetRelay(ctx context.Context, r Relay) error {
	r.Host = strings.ToLower(strings.TrimSpace(r.Host))
	r.Username = strings.TrimSpace(r.Username)
	switch {
	case r.Host == "" || strings.ContainsAny(r.Host, " /:@"):
		return Invalid("A relay needs a host name, without a port.")
	case r.Port < 1 || r.Port > 65535:
		return Invalid("A port is between 1 and 65535.")
	case r.Security != SecurityStartTLS && r.Security != SecurityImplicit:
		return Invalid("Encryption is STARTTLS or TLS.")
	}
	if r.Username != "" && r.Password == "" {
		existing, err := s.Relay(ctx)
		if err != nil {
			return err
		}
		if existing == nil || existing.Username != r.Username || existing.Password == "" {
			return Invalid("Give the relay's password.")
		}
		r.Password = existing.Password
	}
	if r.Username == "" && r.Password != "" {
		return Invalid("A password needs a username.")
	}
	var sealed []byte
	if r.Password != "" {
		if s.sealer == nil {
			return errors.New("set relay: no key to seal the password with")
		}
		sealed = s.sealer.Seal([]byte(r.Password), s.relayAAD())
	}
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO relay (id, host, port, security, username, password_sealed) VALUES (1, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET host = excluded.host, port = excluded.port, security = excluded.security,
		   username = excluded.username, password_sealed = excluded.password_sealed`,
		r.Host, r.Port, r.Security, r.Username, sealed)
	if err != nil {
		return fmt.Errorf("set relay: %w", err)
	}
	s.changed()
	return nil
}

// ClearRelay goes back to delivering directly.
func (s *Store) ClearRelay(ctx context.Context) error {
	if _, err := s.writer.ExecContext(ctx, `DELETE FROM relay`); err != nil {
		return fmt.Errorf("clear relay: %w", err)
	}
	s.changed()
	return nil
}

// SetDKIM stores a domain's signing key.
func (s *Store) SetDKIM(ctx context.Context, domainID, selector, keyPEM string) error {
	if _, err := s.writer.ExecContext(ctx,
		`UPDATE domains SET dkim_selector = ?, dkim_key = ? WHERE id = ?`, selector, keyPEM, domainID); err != nil {
		return fmt.Errorf("set dkim: %w", err)
	}
	s.changed()
	return nil
}

// OutboundRecipient is one recipient of a queued message.
type OutboundRecipient struct {
	ID        int64
	MessageID int64
	Sender    string
	UserID    string
	Recipient string
	Attempts  int
	CreatedAt time.Time
	LastError string
	NextAt    time.Time
}

// Enqueue queues a message for recipients on other servers. Due at once.
func (s *Store) Enqueue(ctx context.Context, sender, userID string, raw []byte, recipients []string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	defer tx.Rollback()
	now := s.Now()
	var user any
	if userID != "" {
		user = userID
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO outbound_messages (sender, user_id, raw, created_at) VALUES (?, ?, ?, ?)`,
		sender, user, raw, unix(now))
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	id, _ := res.LastInsertId()
	for _, rcpt := range recipients {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO outbound_recipients (message_id, recipient, next_at) VALUES (?, ?, ?)`,
			id, rcpt, unix(now)); err != nil {
			return fmt.Errorf("enqueue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	s.changed()
	return nil
}

const outboundColumns = `r.id, r.message_id, m.sender, coalesce(m.user_id, ''), r.recipient, r.attempts,
	m.created_at, r.last_error, r.next_at`

func scanOutbound(rows *sql.Rows) ([]OutboundRecipient, error) {
	defer rows.Close()
	var out []OutboundRecipient
	for rows.Next() {
		var (
			o              OutboundRecipient
			created, after int64
		)
		if err := rows.Scan(&o.ID, &o.MessageID, &o.Sender, &o.UserID, &o.Recipient, &o.Attempts,
			&created, &o.LastError, &after); err != nil {
			return nil, err
		}
		o.CreatedAt, o.NextAt = fromUnix(created), fromUnix(after)
		out = append(out, o)
	}
	return out, rows.Err()
}

// DueOutbound returns recipients whose time has come, grouped by message.
func (s *Store) DueOutbound(ctx context.Context, limit int) ([]OutboundRecipient, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT `+outboundColumns+` FROM outbound_recipients r JOIN outbound_messages m ON m.id = r.message_id
		  WHERE r.next_at <= ? ORDER BY r.message_id, r.id LIMIT ?`, unix(s.Now()), limit)
	if err != nil {
		return nil, fmt.Errorf("due outbound: %w", err)
	}
	return scanOutbound(rows)
}

// Outbound lists the whole queue, oldest first: what the admin interface shows.
func (s *Store) Outbound(ctx context.Context) ([]OutboundRecipient, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT `+outboundColumns+` FROM outbound_recipients r JOIN outbound_messages m ON m.id = r.message_id
		  ORDER BY r.message_id, r.id`)
	if err != nil {
		return nil, fmt.Errorf("outbound: %w", err)
	}
	return scanOutbound(rows)
}

// NextOutbound is when the next recipient falls due, if any is queued.
func (s *Store) NextOutbound(ctx context.Context) (time.Time, bool, error) {
	var next sql.NullInt64
	if err := s.reader.QueryRowContext(ctx, `SELECT min(next_at) FROM outbound_recipients`).Scan(&next); err != nil {
		return time.Time{}, false, fmt.Errorf("next outbound: %w", err)
	}
	if !next.Valid {
		return time.Time{}, false, nil
	}
	return fromUnix(next.Int64), true, nil
}

// OutboundRaw is a queued message's bytes.
func (s *Store) OutboundRaw(ctx context.Context, messageID int64) ([]byte, error) {
	var raw []byte
	err := s.reader.QueryRowContext(ctx, `SELECT raw FROM outbound_messages WHERE id = ?`, messageID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound("There is no such message.")
	}
	return raw, err
}

// FinishOutbound removes recipients that were delivered or given up on, and a message once it has
// none left.
func (s *Store) FinishOutbound(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("finish outbound: %w", err)
	}
	defer tx.Rollback()
	list := int64sJSON(ids)
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM outbound_recipients WHERE id IN (SELECT value FROM json_each(?))`, list); err != nil {
		return fmt.Errorf("finish outbound: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM outbound_messages WHERE id NOT IN (SELECT message_id FROM outbound_recipients)`); err != nil {
		return fmt.Errorf("finish outbound: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("finish outbound: %w", err)
	}
	s.changed()
	return nil
}

// DeferOutbound records a failed attempt and when to try again.
func (s *Store) DeferOutbound(ctx context.Context, id int64, next time.Time, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	_, err := s.writer.ExecContext(ctx,
		`UPDATE outbound_recipients SET attempts = attempts + 1, next_at = ?, last_error = ? WHERE id = ?`,
		unix(next), reason, id)
	if err != nil {
		return fmt.Errorf("defer outbound: %w", err)
	}
	return nil
}

// RetryOutbound makes everything queued due now, for the admin who has just fixed the relay.
func (s *Store) RetryOutbound(ctx context.Context) error {
	_, err := s.writer.ExecContext(ctx, `UPDATE outbound_recipients SET next_at = ?`, unix(s.Now()))
	return err
}

func int64sJSON(ids []int64) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprint(&b, id)
	}
	b.WriteByte(']')
	return b.String()
}
