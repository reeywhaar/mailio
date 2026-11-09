package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"mailio/internal/ids"
)

// Password is one of a user's passwords. Each has its own login, the user's address with a tag,
// and a label, so one device's can be shown or deleted without touching the rest.
type Password struct {
	ID     string
	UserID string
	Label  string
	// Login is the tag that signs in with this password: inbox+k3x9@reey.ru for "k3x9", the plain
	// address for "".
	Login     string
	CreatedAt time.Time
	// LastUsedAt is when it last signed in, to the hour; zero when it never has.
	LastUsedAt time.Time
}

// DefaultPasswordLabel is the label of the password a user is created with, whose login is the
// plain address.
const DefaultPasswordLabel = "default"

// loginLen is a login tag's length. Not a secret, only what tells a user's passwords apart, so
// four characters are plenty.
const loginLen = 4

// LoginAddress is what signs in with a password: the address, tagged unless the login is plain.
func (p *Password) LoginAddress(u *User) string {
	if p.Login == "" {
		return u.Address()
	}
	return u.Local + "+" + p.Login + "@" + u.Domain
}

func newLogin() string {
	b := make([]byte, loginLen)
	rand.Read(b)
	for i := range b {
		b[i] = ids.Alphabet[int(b[i])%len(ids.Alphabet)]
	}
	return string(b)
}

// usedEvery is how stale last_used_at may get. Updating it on every IMAP login would be a write
// per connection for a date shown to the day.
const usedEvery = time.Hour

func passwordLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if label == "" || len([]rune(label)) > 64 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return "", Invalid("A label is up to 64 characters: what the password is for, like iPhone.")
	}
	return label, nil
}

const passwordColumns = `id, user_id, label, login, created_at, coalesce(last_used_at, 0)`

func scanPassword(row interface{ Scan(...any) error }) (*Password, error) {
	var (
		p             Password
		created, used int64
	)
	if err := row.Scan(&p.ID, &p.UserID, &p.Label, &p.Login, &created, &used); err != nil {
		return nil, err
	}
	p.CreatedAt = fromUnix(created)
	if used != 0 {
		p.LastUsedAt = fromUnix(used)
	}
	return &p, nil
}

// Passwords returns every user's passwords, by user and then age: what the admin interface lists.
func (s *Store) Passwords(ctx context.Context) (map[string][]Password, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+passwordColumns+` FROM user_passwords ORDER BY user_id, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("passwords: %w", err)
	}
	defer rows.Close()
	out := map[string][]Password{}
	for rows.Next() {
		p, err := scanPassword(rows)
		if err != nil {
			return nil, fmt.Errorf("passwords: %w", err)
		}
		out[p.UserID] = append(out[p.UserID], *p)
	}
	return out, rows.Err()
}

// AddPassword gives a user another password, with a login of its own.
func (s *Store) AddPassword(ctx context.Context, userID, label, password string) (*Password, error) {
	label, err := passwordLabel(label)
	if err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	var taken int
	if err := s.reader.QueryRowContext(ctx,
		`SELECT count(*) FROM user_passwords WHERE user_id = ? AND label = ?`, userID, label).Scan(&taken); err != nil {
		return nil, fmt.Errorf("add password: %w", err)
	}
	if taken > 0 {
		return nil, Conflict("%s already has a password called %s.", u.Address(), label)
	}
	// A new login until one is free: four characters, so a clash is rare and another try is cheap.
	for range 8 {
		p, err := insertPassword(ctx, s.writer, userID, label, newLogin(), hash, s.Now())
		if isUnique(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("add password: %w", err)
		}
		s.changed()
		return p, nil
	}
	return nil, Conflict("No free login was found. Try again.")
}

func insertPassword(ctx context.Context, db execer, userID, label, login, hash string, now time.Time) (*Password, error) {
	p := &Password{ID: ids.New(ids.Password, now.UnixMilli()), UserID: userID, Label: label, Login: login, CreatedAt: now}
	_, err := db.ExecContext(ctx,
		`INSERT INTO user_passwords (id, user_id, label, login, hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, userID, label, login, hash, unix(now))
	if err != nil {
		return nil, err
	}
	return p, nil
}

// DeletePassword removes one password. The last one may go too: the user keeps receiving mail
// and nobody can sign in as it until another is added.
func (s *Store) DeletePassword(ctx context.Context, userID, id string) error {
	res, err := s.writer.ExecContext(ctx, `DELETE FROM user_passwords WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("There is no such password.")
	}
	s.changed()
	return nil
}

// passwordFor is the hash a login signs in with, or ErrNotFound.
func (s *Store) passwordFor(ctx context.Context, userID, login string) (id, hash string, lastUsed time.Time, err error) {
	var used int64
	err = s.reader.QueryRowContext(ctx,
		`SELECT id, hash, coalesce(last_used_at, 0) FROM user_passwords WHERE user_id = ? AND login = ?`,
		userID, login).Scan(&id, &hash, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, ErrNotFound
	}
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("authenticate: %w", err)
	}
	if used != 0 {
		lastUsed = fromUnix(used)
	}
	return id, hash, lastUsed, nil
}

// touchPassword records a sign-in. Not a change for the backup loop: it is the process noticing
// itself. Best effort, and a second at most, so a busy writer never delays a login.
func (s *Store) touchPassword(ctx context.Context, id string, last time.Time) {
	now := s.Now()
	if now.Sub(last) < usedEvery {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	s.writer.ExecContext(ctx, `UPDATE user_passwords SET last_used_at = ? WHERE id = ?`, unix(now), id)
}
