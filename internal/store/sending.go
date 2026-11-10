package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"mailio/internal/ids"
)

// How the connection to a relay is encrypted. There is no third option: a password crossing the
// network in the clear is not a choice somebody should make by accident.
const (
	SecurityStartTLS = "starttls"
	SecurityImplicit = "implicit"
)

// Relay is a server outbound mail can be handed to, for a host whose provider blocks port 25.
// Its label is unique, and what a domain or the default names it by in the admin interface.
type Relay struct {
	ID       string
	Label    string
	Host     string
	Port     int
	Security string
	Username string
	// Password is only filled by RelayByID, which opens it; HasPassword says whether there is one.
	Password    string
	HasPassword bool
	CreatedAt   time.Time
}

// legacyRelayAAD is what the single relay before this list sealed its password to.
const legacyRelayAAD = "relay-password"

func relayAAD(id string) []byte { return []byte("relay-password:" + id) }

const relayColumns = `id, label, host, port, security, username, password_sealed IS NOT NULL, created_at`

func scanRelay(row interface{ Scan(...any) error }) (*Relay, error) {
	var (
		r       Relay
		created int64
	)
	if err := row.Scan(&r.ID, &r.Label, &r.Host, &r.Port, &r.Security, &r.Username, &r.HasPassword, &created); err != nil {
		return nil, err
	}
	r.CreatedAt = fromUnix(created)
	return &r, nil
}

// Relays lists the relays by label, without their passwords.
func (s *Store) Relays(ctx context.Context) ([]Relay, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+relayColumns+` FROM relays ORDER BY label`)
	if err != nil {
		return nil, fmt.Errorf("relays: %w", err)
	}
	defer rows.Close()
	var out []Relay
	for rows.Next() {
		r, err := scanRelay(rows)
		if err != nil {
			return nil, fmt.Errorf("relays: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// RelayByID returns one relay with its password opened, for handing mail to it.
func (s *Store) RelayByID(ctx context.Context, id string) (*Relay, error) {
	var (
		sealed []byte
		legacy bool
	)
	row := s.reader.QueryRowContext(ctx, `SELECT `+relayColumns+`, password_sealed, legacy_aad FROM relays WHERE id = ?`, id)
	var (
		r       Relay
		created int64
	)
	err := row.Scan(&r.ID, &r.Label, &r.Host, &r.Port, &r.Security, &r.Username, &r.HasPassword, &created, &sealed, &legacy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound("There is no such relay.")
	}
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}
	r.CreatedAt = fromUnix(created)
	if sealed != nil {
		if s.sealer == nil {
			return nil, errors.New("relay: the password is sealed and this store has no key")
		}
		aad := relayAAD(id)
		if legacy {
			aad = []byte(legacyRelayAAD)
		}
		plain, err := s.sealer.Open(sealed, aad)
		if err != nil {
			return nil, Invalid("The password of %s was sealed with another MAILIO_SECRET_KEY. Enter it again.", r.Label)
		}
		r.Password = string(plain)
	}
	return &r, nil
}

// checkRelay validates a relay, and with an empty password keeps existing's for the same
// username, so a port can be corrected without retyping a credential the form was never shown.
func checkRelay(r *Relay, existing *Relay) error {
	r.Label = strings.TrimSpace(r.Label)
	r.Host = strings.ToLower(strings.TrimSpace(r.Host))
	r.Username = strings.TrimSpace(r.Username)
	switch {
	case r.Label == "" || len([]rune(r.Label)) > 64:
		return Invalid("A relay needs a label, up to 64 characters.")
	case r.Host == "" || strings.ContainsAny(r.Host, " /:@"):
		return Invalid("A relay needs a host name, without a port.")
	case r.Port < 1 || r.Port > 65535:
		return Invalid("A port is between 1 and 65535.")
	case r.Security != SecurityStartTLS && r.Security != SecurityImplicit:
		return Invalid("Encryption is STARTTLS or TLS.")
	}
	if r.Username != "" && r.Password == "" {
		if existing == nil || existing.Username != r.Username || existing.Password == "" {
			return Invalid("Give the relay's password.")
		}
		r.Password = existing.Password
	}
	if r.Username == "" && r.Password != "" {
		return Invalid("A password needs a username.")
	}
	return nil
}

func (s *Store) sealRelay(r *Relay) ([]byte, error) {
	if r.Password == "" {
		return nil, nil
	}
	if s.sealer == nil {
		return nil, errors.New("relay: no key to seal the password with")
	}
	return s.sealer.Seal([]byte(r.Password), relayAAD(r.ID)), nil
}

func relayTaken(label string) error { return Conflict("There is already a relay called %s.", label) }

// CreateRelay adds a relay.
func (s *Store) CreateRelay(ctx context.Context, r Relay) (*Relay, error) {
	if err := checkRelay(&r, nil); err != nil {
		return nil, err
	}
	now := s.Now()
	r.ID, r.CreatedAt = ids.New(ids.Relay, now.UnixMilli()), now
	sealed, err := s.sealRelay(&r)
	if err != nil {
		return nil, err
	}
	_, err = s.writer.ExecContext(ctx,
		`INSERT INTO relays (id, label, host, port, security, username, password_sealed, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Label, r.Host, r.Port, r.Security, r.Username, sealed, unix(now))
	if isUnique(err) {
		return nil, relayTaken(r.Label)
	}
	if err != nil {
		return nil, fmt.Errorf("create relay: %w", err)
	}
	s.changed()
	r.HasPassword, r.Password = sealed != nil, ""
	return &r, nil
}

// UpdateRelay rewrites a relay. Its password is sealed afresh to its id.
func (s *Store) UpdateRelay(ctx context.Context, id string, r Relay) (*Relay, error) {
	existing, err := s.RelayByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkRelay(&r, existing); err != nil {
		return nil, err
	}
	r.ID, r.CreatedAt = id, existing.CreatedAt
	sealed, err := s.sealRelay(&r)
	if err != nil {
		return nil, err
	}
	_, err = s.writer.ExecContext(ctx,
		`UPDATE relays SET label = ?, host = ?, port = ?, security = ?, username = ?, password_sealed = ?,
		   legacy_aad = 0 WHERE id = ?`,
		r.Label, r.Host, r.Port, r.Security, r.Username, sealed, id)
	if isUnique(err) {
		return nil, relayTaken(r.Label)
	}
	if err != nil {
		return nil, fmt.Errorf("update relay: %w", err)
	}
	s.changed()
	r.HasPassword, r.Password = sealed != nil, ""
	return &r, nil
}

// DeleteRelay removes a relay nothing sends through.
func (s *Store) DeleteRelay(ctx context.Context, id string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete relay: %w", err)
	}
	defer tx.Rollback()
	r, err := scanRelay(tx.QueryRowContext(ctx, `SELECT `+relayColumns+` FROM relays WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return NotFound("There is no such relay.")
	}
	if err != nil {
		return fmt.Errorf("delete relay: %w", err)
	}
	var isDefault int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM settings WHERE relay_id = ?`, id).Scan(&isDefault); err != nil {
		return fmt.Errorf("delete relay: %w", err)
	}
	if isDefault > 0 {
		return Conflict("%s is the default. Choose another default first.", r.Label)
	}
	var user string
	err = tx.QueryRowContext(ctx,
		`SELECT name FROM (SELECT name, 0 AS kind FROM domains WHERE relay_id = ?1
		                   UNION ALL
		                   SELECT u.local_part || '@' || d.name, 1 FROM accounts u JOIN domains d ON d.id = u.domain_id
		                    WHERE u.relay_id = ?1)
		  ORDER BY kind, name LIMIT 1`, id).Scan(&user)
	if err == nil {
		return Conflict("%s sends through %s. Change that first.", user, r.Label)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("delete relay: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relays WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete relay: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete relay: %w", err)
	}
	s.changed()
	return nil
}

// How a domain's or an account's mail goes out when it does not follow the one above.
const (
	// ViaBuiltIn delivers to each recipient's MX directly.
	ViaBuiltIn = "builtin"
	// ViaRelay hands it to the domain's chosen relay.
	ViaRelay = "relay"
)

// SetDomainVia chooses how a domain's mail goes out: "" follows the default, ViaBuiltIn delivers
// directly, and a relay's id sends through it.
func (s *Store) SetDomainVia(ctx context.Context, domainID, choice string) error {
	via, relay, err := s.via(ctx, choice)
	if err != nil {
		return err
	}
	res, err := s.writer.ExecContext(ctx,
		`UPDATE domains SET send_via = ?, relay_id = ? WHERE id = ? AND (send_via IS NOT ? OR relay_id IS NOT ?)`,
		via, relay, domainID, via, relay)
	if err != nil {
		return fmt.Errorf("set domain relay: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
		return nil
	}
	_, err = s.DomainByID(ctx, domainID)
	return err
}

// SetAccountVia chooses how an account's mail goes out: "" follows its domain's, ViaBuiltIn
// delivers directly, and a relay's id goes through that relay.
func (s *Store) SetAccountVia(ctx context.Context, accountID, choice string) error {
	via, relay, err := s.via(ctx, choice)
	if err != nil {
		return err
	}
	res, err := s.writer.ExecContext(ctx,
		`UPDATE accounts SET send_via = ?, relay_id = ? WHERE id = ? AND (send_via IS NOT ? OR relay_id IS NOT ?)`,
		via, relay, accountID, via, relay)
	if err != nil {
		return fmt.Errorf("set account relay: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
		return nil
	}
	_, err = s.AccountByID(ctx, accountID)
	return err
}

// via is a choice of how mail goes out as the columns hold it: both null for "", which follows
// the one above.
func (s *Store) via(ctx context.Context, choice string) (via, relay any, err error) {
	switch choice {
	case "":
		return nil, nil, nil
	case ViaBuiltIn:
		return ViaBuiltIn, nil, nil
	}
	if _, err := s.RelayByID(ctx, choice); err != nil {
		return nil, nil, err
	}
	return ViaRelay, choice, nil
}

// RelayFor is the relay an account's mail goes through, or nil to deliver directly: the
// account's own choice, else its domain's, else the default. Mail with no account follows the
// sending domain's, and a domain mailio does not receive for follows the default.
func (s *Store) RelayFor(ctx context.Context, accountID, domain string) (*Relay, error) {
	via, relayID := "", ""
	if accountID != "" {
		a, err := s.AccountByID(ctx, accountID)
		switch {
		case err == nil:
			via, relayID, domain = a.Via, a.RelayID, a.Domain
		case !errors.Is(err, ErrNotFound):
			return nil, err
		}
	}
	if via == "" {
		d, err := s.DomainByName(ctx, domain)
		switch {
		case err == nil:
			via, relayID = d.Via, d.RelayID
		case !errors.Is(err, ErrNotFound):
			return nil, err
		}
	}
	switch via {
	case ViaBuiltIn:
		return nil, nil
	case "":
		set, err := s.Settings(ctx)
		if err != nil {
			return nil, err
		}
		relayID = set.RelayID
	}
	if relayID == "" {
		return nil, nil
	}
	return s.RelayByID(ctx, relayID)
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
	AccountID string
	Recipient string
	Attempts  int
	CreatedAt time.Time
	LastError string
	NextAt    time.Time
	// ReleaseAt is when a held message is sent, or zero for one sent at once.
	ReleaseAt time.Time
}

// Since is when the recipient's tries count from: its release, for held mail.
func (o *OutboundRecipient) Since() time.Time {
	if o.ReleaseAt.After(o.CreatedAt) {
		return o.ReleaseAt
	}
	return o.CreatedAt
}

// Enqueue queues a message for recipients on other servers. Due at once.
func (s *Store) Enqueue(ctx context.Context, sender, accountID string, raw []byte, recipients []string) error {
	return s.Hold(ctx, sender, accountID, raw, recipients, time.Time{})
}

// Hold queues a message until release; a zero or past one is at once.
func (s *Store) Hold(ctx context.Context, sender, accountID string, raw []byte, recipients []string, release time.Time) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	defer tx.Rollback()
	now := s.Now()
	var account any
	if accountID != "" {
		account = accountID
	}
	due, held := now, any(nil)
	if release.After(now) {
		due, held = release, unix(release)
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO outbound_messages (sender, account_id, raw, created_at, release_at) VALUES (?, ?, ?, ?, ?)`,
		sender, account, raw, unix(now), held)
	if err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	id, _ := res.LastInsertId()
	for _, rcpt := range recipients {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO outbound_recipients (message_id, recipient, next_at) VALUES (?, ?, ?)`,
			id, rcpt, unix(due)); err != nil {
			return fmt.Errorf("enqueue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	s.changed()
	return nil
}

const outboundColumns = `r.id, r.message_id, m.sender, coalesce(m.account_id, ''), r.recipient, r.attempts,
	m.created_at, r.last_error, r.next_at, coalesce(m.release_at, 0)`

func scanOutbound(rows *sql.Rows) ([]OutboundRecipient, error) {
	defer rows.Close()
	var out []OutboundRecipient
	for rows.Next() {
		var (
			o                       OutboundRecipient
			created, after, release int64
		)
		if err := rows.Scan(&o.ID, &o.MessageID, &o.Sender, &o.AccountID, &o.Recipient, &o.Attempts,
			&created, &o.LastError, &after, &release); err != nil {
			return nil, err
		}
		o.CreatedAt, o.NextAt = fromUnix(created), fromUnix(after)
		if release != 0 {
			o.ReleaseAt = fromUnix(release)
		}
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
	// Held mail waits for its time: its sender chose it.
	now := unix(s.Now())
	_, err := s.writer.ExecContext(ctx,
		`UPDATE outbound_recipients SET next_at = ?1
		  WHERE message_id NOT IN (SELECT id FROM outbound_messages WHERE release_at > ?1)`, now)
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
