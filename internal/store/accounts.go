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

const accountColumns = `u.id, u.local_part, d.name, u.domain_id, u.created_at, u.incoming, coalesce(u.forward_account_id, '')`
const accountFrom = ` FROM accounts u JOIN domains d ON d.id = u.domain_id`

func scanAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var (
		u       Account
		created int64
	)
	if err := row.Scan(&u.ID, &u.Local, &u.Domain, &u.DomainID, &created, &u.Incoming, &u.ForwardAccountID); err != nil {
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
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	s.changed()
	return u, nil
}

func insertAccount(ctx context.Context, db execer, u *Account) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO accounts (id, domain_id, local_part, created_at) VALUES (?, ?, ?, ?)`,
		u.ID, u.DomainID, u.Local, unix(u.CreatedAt))
	if isUnique(err) {
		return Conflict("%s is taken.", u.Address())
	}
	if err != nil {
		return fmt.Errorf("create account: %w", err)
	}
	return nil
}

// Authenticate signs in with a login and its password. The login is the account's address, plain
// for the "default" password and tagged for each other: inbox+k3x9@reey.ru names exactly one
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
	var junkFor string
	err = tx.QueryRowContext(ctx,
		`SELECT name FROM domains WHERE unknown_own AND unknown_account_id = ?
		 UNION ALL SELECT 'every domain by default' FROM settings WHERE unknown_account_id = ?
		 LIMIT 1`, id, id).Scan(&junkFor)
	if err == nil {
		return Conflict("%s gets the junk for %s. Change that first.", u.Address(), junkFor)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("delete account: %w", err)
	}
	from, err := accountWhere(ctx, tx, "u.forward_account_id = ? ORDER BY d.name, u.local_part LIMIT 1", id)
	if err == nil {
		return Conflict("%s forwards to %s. Change that first.", from.Address(), u.Address())
	}
	if !errors.Is(err, ErrNotFound) {
		return err
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
	var target any
	if forwardTo != "" {
		if forwardTo == id {
			return Invalid("An account cannot forward to itself.")
		}
		// Following the target's own forwards back here would be a circle.
		next := forwardTo
		for hops := 0; next != ""; hops++ {
			t, err := s.AccountByID(ctx, next)
			if err != nil {
				return err
			}
			if t.ID == id || hops > maxForwards {
				return Conflict("That would forward in a circle.")
			}
			next = t.ForwardAccountID
		}
		target = forwardTo
	}
	res, err := s.writer.ExecContext(ctx,
		`UPDATE accounts SET incoming = ?, forward_account_id = ?
		  WHERE id = ? AND (incoming <> ? OR forward_account_id IS NOT ?)`, in, target, id, in, target)
	if err != nil {
		return fmt.Errorf("set incoming: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
		return nil
	}
	_, err = s.AccountByID(ctx, id)
	return err
}

// maxForwards bounds how far mail follows forwards, so a circle made some other way ends.
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
