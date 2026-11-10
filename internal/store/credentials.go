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

// Password is one of an account's credentials. Each has its own login, the account's address with a tag,
// and a label, so one device's can be shown or deleted without touching the rest.
type Credential struct {
	ID        string
	AccountID string
	Label     string
	// Login is the tag that signs in with this password: inbox+k3x9@reey.ru for "k3x9", the plain
	// address for "".
	Login     string
	CreatedAt time.Time
	// LastUsedAt is when it last signed in, to the hour; zero when it never has.
	LastUsedAt time.Time
}

// DefaultCredentialLabel is the label of the password an account is created with, whose login is the
// plain address.
const DefaultCredentialLabel = "default"

// loginLen is a login tag's length. Not a secret, only what tells an account's credentials apart, so
// four characters are plenty.
const loginLen = 4

// LoginAddress is what signs in with a password: the address, tagged unless the login is plain.
func (p *Credential) LoginAddress(u *Account) string {
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

func credentialLabel(raw string) (string, error) {
	label := strings.TrimSpace(raw)
	if label == "" || len([]rune(label)) > 64 || strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return "", Invalid("A label is up to 64 characters: what the password is for, like iPhone.")
	}
	return label, nil
}

const credentialColumns = `id, account_id, label, login, created_at, coalesce(last_used_at, 0)`

func scanCredential(row interface{ Scan(...any) error }) (*Credential, error) {
	var (
		p             Credential
		created, used int64
	)
	if err := row.Scan(&p.ID, &p.AccountID, &p.Label, &p.Login, &created, &used); err != nil {
		return nil, err
	}
	p.CreatedAt = fromUnix(created)
	if used != 0 {
		p.LastUsedAt = fromUnix(used)
	}
	return &p, nil
}

// Passwords returns every account's passwords, by account and then age: what the admin interface lists.
func (s *Store) Credentials(ctx context.Context) (map[string][]Credential, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+credentialColumns+` FROM credentials ORDER BY account_id, created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("passwords: %w", err)
	}
	defer rows.Close()
	out := map[string][]Credential{}
	for rows.Next() {
		p, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("passwords: %w", err)
		}
		out[p.AccountID] = append(out[p.AccountID], *p)
	}
	return out, rows.Err()
}

// AddCredential gives an account another password, with a login of its own.
func (s *Store) AddCredential(ctx context.Context, accountID, label, password string) (*Credential, error) {
	label, err := credentialLabel(label)
	if err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	u, err := s.AccountByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	var taken int
	if err := s.reader.QueryRowContext(ctx,
		`SELECT count(*) FROM credentials WHERE account_id = ? AND label = ?`, accountID, label).Scan(&taken); err != nil {
		return nil, fmt.Errorf("add password: %w", err)
	}
	if taken > 0 {
		return nil, Conflict("%s already has a password called %s.", u.Address(), label)
	}
	// A new login until one is free: four characters, so a clash is rare and another try is cheap.
	for range 8 {
		p, err := insertCredential(ctx, s.writer, accountID, label, newLogin(), hash, s.Now())
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

func insertCredential(ctx context.Context, db execer, accountID, label, login, hash string, now time.Time) (*Credential, error) {
	p := &Credential{ID: ids.New(ids.Credential, now.UnixMilli()), AccountID: accountID, Label: label, Login: login, CreatedAt: now}
	_, err := db.ExecContext(ctx,
		`INSERT INTO credentials (id, account_id, label, login, hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, accountID, label, login, hash, unix(now))
	if err != nil {
		return nil, err
	}
	return p, nil
}

// GeneratedLength is how long a generated password is: 24 letters and digits, about 143 bits.
const GeneratedLength = 24

const generatedAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// GeneratePassword makes an account's password. Letters and digits only, with the ones a person
// copying it by eye confuses left out, so it survives being read off a screen into a phone.
func GeneratePassword() string {
	b := make([]byte, GeneratedLength)
	limit := 256 - 256%len(generatedAlphabet)
	for i := 0; i < len(b); {
		var c [1]byte
		rand.Read(c[:])
		if int(c[0]) >= limit {
			continue
		}
		b[i] = generatedAlphabet[int(c[0])%len(generatedAlphabet)]
		i++
	}
	return string(b)
}

// ResetCredential replaces one password, keeping its label and login: the device it was for gets
// the new one, and nothing else changes.
func (s *Store) ResetCredential(ctx context.Context, accountID, id, password string) (*Credential, error) {
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	res, err := s.writer.ExecContext(ctx,
		`UPDATE credentials SET hash = ?, created_at = ?, last_used_at = NULL WHERE id = ? AND account_id = ?`,
		hash, unix(s.Now()), id, accountID)
	if err != nil {
		return nil, fmt.Errorf("reset password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, NotFound("There is no such password.")
	}
	s.changed()
	p, err := scanCredential(s.writer.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM credentials WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("reset password: %w", err)
	}
	return p, nil
}

// DeleteCredential removes one password. The last one may go too: the account keeps receiving mail
// and nobody can sign in as it until another is added.
func (s *Store) DeleteCredential(ctx context.Context, accountID, id string) error {
	res, err := s.writer.ExecContext(ctx, `DELETE FROM credentials WHERE id = ? AND account_id = ?`, id, accountID)
	if err != nil {
		return fmt.Errorf("delete password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("There is no such password.")
	}
	s.changed()
	return nil
}

// credentialFor is the hash a login signs in with, or ErrNotFound.
func (s *Store) credentialFor(ctx context.Context, accountID, login string) (id, hash string, lastUsed time.Time, err error) {
	var used int64
	err = s.reader.QueryRowContext(ctx,
		`SELECT id, hash, coalesce(last_used_at, 0) FROM credentials WHERE account_id = ? AND login = ?`,
		accountID, login).Scan(&id, &hash, &used)
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

// touchCredential records a sign-in. Not a change for the backup loop: it is the process noticing
// itself. Best effort, and a second at most, so a busy writer never delays a login.
func (s *Store) touchCredential(ctx context.Context, id string, last time.Time) {
	now := s.Now()
	if now.Sub(last) < usedEvery {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	s.writer.ExecContext(ctx, `UPDATE credentials SET last_used_at = ? WHERE id = ?`, unix(now), id)
}
