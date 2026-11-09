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

// User is an address mail is delivered to, and the passwords that sign in as it: to read the mail
// and to send as it. Never to the admin interface, which is an Admin's.
type User struct {
	ID        string
	Local     string
	Domain    string
	DomainID  string
	CreatedAt time.Time
	// Incoming is what mail to the user does; ForwardUserID is set exactly when it forwards.
	Incoming      Incoming
	ForwardUserID string
}

// Incoming is what a user does with mail sent to it.
type Incoming string

const (
	// IncomingKeep delivers it to the user's own mailbox.
	IncomingKeep Incoming = "keep"
	// IncomingReject refuses it while the sender is still connected: an address that only sends.
	IncomingReject Incoming = "reject"
	// IncomingForward delivers it to another user's mailbox instead.
	IncomingForward Incoming = "forward"
)

func (i Incoming) valid() bool {
	return i == IncomingKeep || i == IncomingReject || i == IncomingForward
}

// Address is what the user signs in with and receives at.
func (u *User) Address() string { return u.Local + "@" + u.Domain }

const userColumns = `u.id, u.local_part, d.name, u.domain_id, u.created_at, u.incoming, coalesce(u.forward_user_id, '')`
const userFrom = ` FROM users u JOIN domains d ON d.id = u.domain_id`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var (
		u       User
		created int64
	)
	if err := row.Scan(&u.ID, &u.Local, &u.Domain, &u.DomainID, &created, &u.Incoming, &u.ForwardUserID); err != nil {
		return nil, err
	}
	u.CreatedAt = fromUnix(created)
	return &u, nil
}

func userWhere(ctx context.Context, db queryer, where string, args ...any) (*User, error) {
	u, err := scanUser(db.QueryRowContext(ctx, `SELECT `+userColumns+userFrom+` WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound("There is no such user.")
	}
	if err != nil {
		return nil, fmt.Errorf("user: %w", err)
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

// CreateUser makes a user at a domain mailio already receives for, with one password labelled
// "default".
func (s *Store) CreateUser(ctx context.Context, addr, password string) (*User, error) {
	a, err := address.ParseUser(addr)
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
	u := &User{ID: ids.New(ids.User, now.UnixMilli()), Local: a.Local, Domain: d.Name, DomainID: d.ID, CreatedAt: now,
		Incoming: IncomingKeep}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	defer tx.Rollback()
	if err := insertUser(ctx, tx, u); err != nil {
		return nil, err
	}
	if _, err := insertPassword(ctx, tx, u.ID, DefaultPasswordLabel, "", hash, now); err != nil {
		return nil, err
	}
	if err := createStandardMailboxes(ctx, tx, u.ID, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	s.changed()
	return u, nil
}

func insertUser(ctx context.Context, db execer, u *User) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, domain_id, local_part, created_at) VALUES (?, ?, ?, ?)`,
		u.ID, u.DomainID, u.Local, unix(u.CreatedAt))
	if isUnique(err) {
		return Conflict("%s is taken.", u.Address())
	}
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// Authenticate signs in with a login and its password. The login is the user's address, plain
// for the "default" password and tagged for each other: inbox+k3x9@reey.ru names exactly one
// password, so a sign-in checks one hash however many the user has.
//
// The same refusal for a wrong password, a missing user and a missing login, and a real hash is
// compared against when nothing matched, so they take the same time. Without that, response
// latency alone is a list of which addresses exist.
func (s *Store) Authenticate(ctx context.Context, login, password string) (*User, error) {
	a, err := address.Parse(login)
	if err != nil {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	u, err := userWhere(ctx, s.reader, "d.name = ? AND u.local_part = ?", a.Domain, a.Name())
	if errors.Is(err, ErrNotFound) {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	id, hash, lastUsed, err := s.passwordFor(ctx, u.ID, a.Tag())
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
	s.touchPassword(ctx, id, lastUsed)
	return u, nil
}

// decoyHash is a real bcrypt hash at the same cost, so a missing user costs what a present one
// does.
var decoyHash = func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("decoy"), bcryptCost)
	return h
}()

// UserByID returns one user.
func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	return userWhere(ctx, s.reader, "u.id = ?", id)
}

// Users returns every user, by domain and then address.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+userColumns+userFrom+` ORDER BY d.name, u.local_part`)
	if err != nil {
		return nil, fmt.Errorf("users: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("users: %w", err)
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// DeleteUser removes a user.
func (s *Store) DeleteUser(ctx context.Context, id string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	defer tx.Rollback()

	u, err := userWhere(ctx, tx, "u.id = ?", id)
	if err != nil {
		return err
	}
	var junkFor string
	err = tx.QueryRowContext(ctx, `SELECT name FROM domains WHERE unknown_user_id = ? ORDER BY name LIMIT 1`, id).Scan(&junkFor)
	if err == nil {
		return Conflict("%s gets the junk for %s. Change that first.", u.Address(), junkFor)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("delete user: %w", err)
	}
	from, err := userWhere(ctx, tx, "u.forward_user_id = ? ORDER BY d.name, u.local_part LIMIT 1", id)
	if err == nil {
		return Conflict("%s forwards to %s. Change that first.", from.Address(), u.Address())
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	s.changed()
	return nil
}

// SetIncoming decides what mail to a user does. forwardTo names the user it goes to for
// IncomingForward, and is empty otherwise.
func (s *Store) SetIncoming(ctx context.Context, id string, in Incoming, forwardTo string) error {
	if !in.valid() {
		return Invalid("Incoming mail is kept, rejected, or forwarded.")
	}
	if (in == IncomingForward) != (forwardTo != "") {
		if in == IncomingForward {
			return Invalid("Choose who gets the mail.")
		}
		return Invalid("Only forwarded mail goes to another user.")
	}
	var target any
	if forwardTo != "" {
		if forwardTo == id {
			return Invalid("A user cannot forward to itself.")
		}
		// Following the target's own forwards back here would be a circle.
		next := forwardTo
		for hops := 0; next != ""; hops++ {
			t, err := s.UserByID(ctx, next)
			if err != nil {
				return err
			}
			if t.ID == id || hops > maxForwards {
				return Conflict("That would forward in a circle.")
			}
			next = t.ForwardUserID
		}
		target = forwardTo
	}
	res, err := s.writer.ExecContext(ctx,
		`UPDATE users SET incoming = ?, forward_user_id = ?
		  WHERE id = ? AND (incoming <> ? OR forward_user_id IS NOT ?)`, in, target, id, in, target)
	if err != nil {
		return fmt.Errorf("set incoming: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
		return nil
	}
	_, err = s.UserByID(ctx, id)
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
