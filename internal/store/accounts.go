package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"mailio/internal/address"
	"mailio/internal/ids"
)

// bcryptCost is 12. A var so tests can lower it: a suite that signs in a few dozen times
// otherwise spends its whole run inside the hash it is not testing.
var bcryptCost = 12

// SetBcryptCost lowers the work factor. Tests only.
func SetBcryptCost(cost int) {
	bcryptCost = cost
	h, _ := bcrypt.GenerateFromPassword([]byte("decoy"), cost)
	decoyHash = h
}

// passwordMaxBytes is bcrypt's own limit, checked rather than silently cut: a longer password
// would otherwise authenticate against any prefix of itself.
const passwordMaxBytes = 72

// Account is an address mail is delivered to, and the credentials that sign in as it: to read the mail
// and to send as it. Never to the admin interface, which is an Admin's.
type Account struct {
	ID        string
	Local     string
	Domain    string
	DomainID  string
	CreatedAt time.Time
	// Incoming is what mail to the account does; ForwardAccountID is set exactly when it forwards.
	Incoming         Incoming
	ForwardAccountID string
	// MaxMessageBytes is the account's own largest message, or 0 to follow its domain's.
	// MessageLimit is the one that applies: its own, its domain's, or the default.
	MaxMessageBytes int64
	MessageLimit    int64
	// Via is how its mail goes out: empty follows its domain's, ViaBuiltIn delivers directly, and
	// ViaRelay goes through RelayID.
	Via     string
	RelayID string
	// TrashDays and JunkDays are how long its Trash and Junk keep a message, 0 forever, or nil to
	// follow its domain's.
	TrashDays *int
	JunkDays  *int
	// Alias is only an address: its mail always goes to ForwardAccountID, which signs in for it,
	// holds its mail and sends as it.
	Alias bool
}

// Incoming is what an account does with mail sent to it.
type Incoming string

const (
	// IncomingKeep delivers it to the account's own mailbox.
	IncomingKeep Incoming = "keep"
	// IncomingReject refuses it while the sender is still connected: an address that only sends.
	IncomingReject Incoming = "reject"
	// IncomingForward delivers it to another account's mailbox instead.
	IncomingForward Incoming = "forward"
)

func (i Incoming) valid() bool {
	return i == IncomingKeep || i == IncomingReject || i == IncomingForward
}

// Address is what the account signs in with and receives at.
func (u *Account) Address() string { return u.Local + "@" + u.Domain }

const accountColumns = `u.id, u.local_part, d.name, u.domain_id, u.created_at, u.incoming, coalesce(u.forward_account_id, ''),
	coalesce(u.max_message_bytes, 0),
	coalesce(u.max_message_bytes, d.max_message_bytes, (SELECT max_message_bytes FROM settings WHERE id = 1)),
	coalesce(u.send_via, ''), coalesce(u.relay_id, ''), u.trash_days, u.junk_days, u.alias`
const accountFrom = ` FROM accounts u JOIN domains d ON d.id = u.domain_id`

func scanAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var (
		u       Account
		created int64
	)
	if err := row.Scan(&u.ID, &u.Local, &u.Domain, &u.DomainID, &created, &u.Incoming, &u.ForwardAccountID, &u.MaxMessageBytes, &u.MessageLimit, &u.Via, &u.RelayID, &u.TrashDays, &u.JunkDays, &u.Alias); err != nil {
		return nil, err
	}
	u.CreatedAt = fromUnix(created)
	return &u, nil
}

func accountWhere(ctx context.Context, db queryer, where string, args ...any) (*Account, error) {
	u, err := scanAccount(db.QueryRowContext(ctx, `SELECT `+accountColumns+accountFrom+` WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound("There is no such account.")
	}
	if err != nil {
		return nil, fmt.Errorf("account: %w", err)
	}
	return u, nil
}

// hashPassword validates and hashes, outside any transaction: bcrypt at cost 12 is the slowest
// thing a write here does, and it should not hold the one writer while it runs.
func hashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(h), err
}

// CreateAccount makes an account at a domain mailio already receives for, with one password labelled
// "default".
func (s *Store) CreateAccount(ctx context.Context, addr, password string) (*Account, error) {
	a, err := address.ParseAccount(addr)
	if err != nil {
		return nil, Invalid("%s", err)
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	d, err := s.DomainByName(ctx, a.Domain)
	if errors.Is(err, ErrNotFound) {
		return nil, Invalid("mailio does not receive for %s. Add the domain first.", a.Domain)
	}
	if err != nil {
		return nil, err
	}
	now := s.Now()
	u := &Account{ID: ids.New(ids.Account, now.UnixMilli()), Local: a.Local, Domain: d.Name, DomainID: d.ID, CreatedAt: now,
		Incoming: IncomingKeep}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	defer tx.Rollback()
	if err := insertAccount(ctx, tx, u); err != nil {
		return nil, err
	}
	if _, err := insertCredential(ctx, tx, u.ID, DefaultCredentialLabel, "", hash, now); err != nil {
		return nil, err
	}
	if err := createStandardMailboxes(ctx, tx, u.ID, now); err != nil {
		return nil, err
	}
	// Read back for the limit it follows, which the database works out.
	if u, err = accountWhere(ctx, tx, "u.id = ?", u.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	s.changed()
	return u, nil
}

// CreateAlias makes an address at a domain mailio receives for whose mail goes to an account
// that keeps its mail, and which that account can send as. That account signs in for it and holds
// its mail.
func (s *Store) CreateAlias(ctx context.Context, addr, accountID string) (*Account, error) {
	a, err := address.ParseAccount(addr)
	if err != nil {
		return nil, Invalid("%s", err)
	}
	d, err := s.DomainByName(ctx, a.Domain)
	if errors.Is(err, ErrNotFound) {
		return nil, Invalid("mailio does not receive for %s. Add the domain first.", a.Domain)
	}
	if err != nil {
		return nil, err
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create alias: %w", err)
	}
	defer tx.Rollback()
	// Read in the transaction that writes, as SetIncoming reads it.
	target, err := accountWhere(ctx, tx, "u.id = ?", accountID)
	if err != nil {
		return nil, err
	}
	if err := keeps(target, "alias's mail"); err != nil {
		return nil, err
	}
	now := s.Now()
	u := &Account{ID: ids.New(ids.Account, now.UnixMilli()), Local: a.Local, Domain: d.Name, DomainID: d.ID, CreatedAt: now,
		Incoming: IncomingForward, ForwardAccountID: target.ID, Alias: true}
	if err := insertAccount(ctx, tx, u); err != nil {
		return nil, err
	}
	if u, err = accountWhere(ctx, tx, "u.id = ?", u.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create alias: %w", err)
	}
	s.changed()
	return u, nil
}

// AliasOf is the alias at addr of the account accountID, which it can send as.
func (s *Store) AliasOf(ctx context.Context, accountID string, addr address.Address) (*Account, error) {
	return accountWhere(ctx, s.reader, "u.alias AND u.forward_account_id = ? AND d.name = ? AND u.local_part = ?",
		accountID, addr.Domain, addr.Name())
}

func insertAccount(ctx context.Context, db execer, u *Account) error {
	var forward any
	if u.ForwardAccountID != "" {
		forward = u.ForwardAccountID
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO accounts (id, domain_id, local_part, created_at, incoming, forward_account_id, alias)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.DomainID, u.Local, unix(u.CreatedAt), u.Incoming, forward, u.Alias)
	if isUnique(err) {
		return Conflict("%s is taken.", u.Address())
	}
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

// Authenticate signs in with a login and its password. The login is the account's address, plain
// for the "default" password and tagged for each other: inbox+k3x9@example.org names exactly one
// password, so a sign-in checks one hash however many the account has.
//
// The same refusal for a wrong password, a missing account and a missing login, and a real hash is
// compared against when nothing matched, so they take the same time. Without that, response
// latency alone is a list of which addresses exist.
func (s *Store) Authenticate(ctx context.Context, login, password string) (*Account, error) {
	a, err := address.Parse(login)
	if err != nil {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	u, err := accountWhere(ctx, s.reader, "d.name = ? AND u.local_part = ?", a.Domain, a.Name())
	if errors.Is(err, ErrNotFound) {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	id, hash, lastUsed, err := s.credentialFor(ctx, u.ID, a.Tag())
	if errors.Is(err, ErrNotFound) {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrNotFound
	}
	s.touchCredential(ctx, id, lastUsed)
	return u, nil
}

// decoyHash is a real bcrypt hash at the same cost, so a missing account costs what a present one
// does.
var decoyHash = func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("decoy"), bcryptCost)
	return h
}()

// AccountByID returns one account.
func (s *Store) AccountByID(ctx context.Context, id string) (*Account, error) {
	return accountWhere(ctx, s.reader, "u.id = ?", id)
}

// Accounts returns every account, by domain and then address.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+accountColumns+accountFrom+` ORDER BY d.name, u.local_part`)
	if err != nil {
		return nil, fmt.Errorf("accounts: %w", err)
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		u, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("accounts: %w", err)
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// DeleteAccount removes an account.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	defer tx.Rollback()

	u, err := accountWhere(ctx, tx, "u.id = ?", id)
	if err != nil {
		return err
	}
	if err := sentOn(ctx, tx, u); err != nil {
		return err
	}
	// What it held to send later goes with it: nobody is left to have wanted it sent.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM outbound_messages WHERE account_id = ? AND release_at > ?`, id, unix(s.Now())); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	s.changed()
	return nil
}

// SetIncoming decides what mail to an account does. forwardTo names the account it goes to for
// IncomingForward, and is empty otherwise.
//
// Mail is only ever sent on once: a forward goes to an account that keeps its mail, and an account
// mail is sent on to keeps it. So there are no chains of forwards, and no circles.
func (s *Store) SetIncoming(ctx context.Context, id string, in Incoming, forwardTo string) error {
	if !in.valid() {
		return Invalid("Incoming mail is kept, rejected, or forwarded.")
	}
	if (in == IncomingForward) != (forwardTo != "") {
		if in == IncomingForward {
			return Invalid("Choose who gets the mail.")
		}
		return Invalid("Only forwarded mail goes to another account.")
	}
	if forwardTo == id {
		return Invalid("An account cannot forward to itself.")
	}
	// Both rules are read in the transaction that writes, so two saves at once cannot each pass
	// with the other's change not yet there.
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set incoming: %w", err)
	}
	defer tx.Rollback()
	u, err := accountWhere(ctx, tx, "u.id = ?", id)
	if err != nil {
		return err
	}
	if u.Alias && in != IncomingForward {
		return Invalid("An alias's mail always goes to an account. Delete the alias to stop it.")
	}
	if in != IncomingKeep {
		if err := sentOn(ctx, tx, u); err != nil {
			return err
		}
	}
	var target any
	if forwardTo != "" {
		t, err := accountWhere(ctx, tx, "u.id = ?", forwardTo)
		if err != nil {
			return err
		}
		if err := keeps(t, "forwarded mail"); err != nil {
			return err
		}
		target = forwardTo
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE accounts SET incoming = ?, forward_account_id = ?
		  WHERE id = ? AND (incoming <> ? OR forward_account_id IS NOT ?)`, in, target, id, in, target)
	if err != nil {
		return fmt.Errorf("set incoming: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set incoming: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
	}
	return nil
}

// SetAccountMessageLimit sets an account's own largest message, or with 0 returns it to its
// domain's.
func (s *Store) SetAccountMessageLimit(ctx context.Context, id string, n int64) error {
	if n != 0 {
		if err := checkLimit(n); err != nil {
			return err
		}
	}
	v := orNull(n)
	res, err := s.writer.ExecContext(ctx,
		`UPDATE accounts SET max_message_bytes = ? WHERE id = ? AND max_message_bytes IS NOT ?`, v, id, v)
	if err != nil {
		return fmt.Errorf("set largest message: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
		return nil
	}
	_, err = s.AccountByID(ctx, id)
	return err
}

// SetAccountKept sets how long an account's Trash or Junk keeps a message, 0 forever, or with nil
// returns it to its domain's.
func (s *Store) SetAccountKept(ctx context.Context, id string, k Kept, days *int) error {
	v, err := k.value(days)
	if err != nil {
		return err
	}
	// k.column is one of two constants, never input.
	res, err := s.writer.ExecContext(ctx,
		`UPDATE accounts SET `+k.column+` = ? WHERE id = ? AND `+k.column+` IS NOT ?`, v, id, v)
	if err != nil {
		return fmt.Errorf("set %s days: %w", k.Name, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
		return nil
	}
	_, err = s.AccountByID(ctx, id)
	return err
}

// sentOn refuses while mail is sent on to the account, forwarded or by a domain's catch-all: it
// has to keep that mail, so it stays, keeping it.
func sentOn(ctx context.Context, tx *sql.Tx, u *Account) error {
	var junkFor string
	err := tx.QueryRowContext(ctx,
		`SELECT name FROM domains WHERE unknown_own AND unknown_account_id = ? ORDER BY name LIMIT 1`, u.ID).Scan(&junkFor)
	if err == nil {
		return Conflict("%s gets the catch-all mail for %s. Change that first.", u.Address(), junkFor)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("mail sent on: %w", err)
	}
	from, err := accountWhere(ctx, tx, "u.forward_account_id = ? ORDER BY d.name, u.local_part LIMIT 1", u.ID)
	if err == nil && from.Alias {
		return Conflict("%s is an alias of %s. Change that first.", from.Address(), u.Address())
	}
	if err == nil {
		return Conflict("%s forwards to %s. Change that first.", from.Address(), u.Address())
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// keeps refuses sending mail on to an account that does not keep its own.
func keeps(a *Account, what string) error {
	if a.Alias {
		return Conflict("%s is an alias. Send the %s straight to the account it belongs to.", a.Address(), what)
	}
	switch a.Incoming {
	case IncomingReject:
		return Conflict("%s is no-reply, and would refuse the %s.", a.Address(), what)
	case IncomingForward:
		return Conflict("%s forwards its own mail on. Send the %s straight to the account it ends up at.", a.Address(), what)
	}
	return nil
}

// maxForwards bounds how far delivery follows forwards, for chains saved before forwards had to
// end where they land.
const maxForwards = 5

// ValidatePassword refuses what bcrypt would silently truncate.
func ValidatePassword(password string) error {
	switch {
	case len(password) < 8:
		return Invalid("A password needs at least 8 characters.")
	case len(password) > passwordMaxBytes:
		return Invalid("That password is longer than %d bytes, which is as much as the hash reads.", passwordMaxBytes)
	}
	return nil
}
