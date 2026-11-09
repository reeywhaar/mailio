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

// User is an address mail is delivered to, and the password that signs in as it — to read the
// mail, to send as it, and to the admin interface when Admin is set.
type User struct {
	ID        string
	Local     string
	Domain    string
	DomainID  string
	Admin     bool
	CreatedAt time.Time
}

// Address is what the user signs in with and receives at.
func (u *User) Address() string { return u.Local + "@" + u.Domain }

const userColumns = `u.id, u.local_part, d.name, u.domain_id, u.admin, u.created_at`
const userFrom = ` FROM users u JOIN domains d ON d.id = u.domain_id`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var (
		u       User
		created int64
	)
	if err := row.Scan(&u.ID, &u.Local, &u.Domain, &u.DomainID, &u.Admin, &created); err != nil {
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

// CreateUser makes a user at a domain mailio already receives for.
func (s *Store) CreateUser(ctx context.Context, addr, password string, admin bool) (*User, error) {
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
	u := &User{ID: ids.New(ids.User, now.UnixMilli()), Local: a.Local, Domain: d.Name, DomainID: d.ID, Admin: admin, CreatedAt: now}
	if err := insertUser(ctx, s.writer, u, hash); err != nil {
		return nil, err
	}
	s.changed()
	return u, nil
}

func insertUser(ctx context.Context, db execer, u *User, hash string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, domain_id, local_part, password_hash, admin, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.DomainID, u.Local, hash, u.Admin, unix(u.CreatedAt))
	if isUnique(err) {
		return Conflict("%s is taken.", u.Address())
	}
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// Authenticate checks a password.
//
// The same refusal for a wrong password and a missing user, and a real hash is compared against
// when no user matched, so the two take the same time. Without that, response latency alone is a
// list of which addresses exist.
func (s *Store) Authenticate(ctx context.Context, addr, password string) (*User, error) {
	a, err := address.ParseUser(addr)
	if err != nil {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	var hash string
	row := s.reader.QueryRowContext(ctx,
		`SELECT u.password_hash, `+userColumns+userFrom+` WHERE d.name = ? AND u.local_part = ?`, a.Domain, a.Local)
	var (
		u       User
		created int64
	)
	err = row.Scan(&hash, &u.ID, &u.Local, &u.Domain, &u.DomainID, &u.Admin, &created)
	if errors.Is(err, sql.ErrNoRows) {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrNotFound
	}
	u.CreatedAt = fromUnix(created)
	return &u, nil
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

// HasAdmin reports whether anybody can sign in to the admin interface. Nothing else decides
// whether serve prints an admin link at startup.
func (s *Store) HasAdmin(ctx context.Context) (bool, error) {
	var n int
	err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE admin = 1`).Scan(&n)
	return n > 0, err
}

// SetPassword replaces a user's password and signs out everywhere they were signed in, which is
// what somebody changing a password they think was seen wants.
func (s *Store) SetPassword(ctx context.Context, id, password string) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	defer tx.Rollback()
	if err := setPassword(ctx, tx, id, hash); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	s.changed()
	return nil
}

func setPassword(ctx context.Context, tx *sql.Tx, id, hash string) error {
	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("There is no such user.")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	return nil
}

// SetAdmin grants or takes away the admin interface.
//
// The last admin keeps it. `mailio admin reset` would let somebody back in, but a click should
// not be what makes that necessary.
func (s *Store) SetAdmin(ctx context.Context, id string, admin bool) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set admin: %w", err)
	}
	defer tx.Rollback()

	u, err := userWhere(ctx, tx, "u.id = ?", id)
	if err != nil {
		return err
	}
	// Already so: no write, no sign-out, and nothing for the backup loop to count.
	if u.Admin == admin {
		return nil
	}
	if u.Admin {
		if err := otherAdmins(ctx, tx, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET admin = ? WHERE id = ?`, admin, id); err != nil {
		return fmt.Errorf("set admin: %w", err)
	}
	if !admin {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			return fmt.Errorf("set admin: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set admin: %w", err)
	}
	s.changed()
	return nil
}

func otherAdmins(ctx context.Context, tx *sql.Tx, id string) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE admin = 1 AND id != ?`, id).Scan(&n); err != nil {
		return fmt.Errorf("count admins: %w", err)
	}
	if n == 0 {
		return Conflict("That is the only admin. Make somebody else an admin first.")
	}
	return nil
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
	if u.Admin {
		if err := otherAdmins(ctx, tx, id); err != nil {
			return err
		}
	}
	var junkFor string
	err = tx.QueryRowContext(ctx, `SELECT name FROM domains WHERE unknown_user_id = ? ORDER BY name LIMIT 1`, id).Scan(&junkFor)
	if err == nil {
		return Conflict("%s gets the junk for %s. Change that first.", u.Address(), junkFor)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("delete user: %w", err)
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
