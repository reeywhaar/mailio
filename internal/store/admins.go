package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"mailio/internal/ids"
)

// Admin is somebody who may sign in to the admin interface: a name and a password. An admin is
// not an account and has no address, so the first one can exist before any domain does.
type Admin struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// adminNamePattern keeps a name something to type at a sign-in form, and never an address: an
// admin who looks like a mailbox invites the belief that it is one.
var adminNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// AdminName lowercases and checks a name.
func AdminName(raw string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if !adminNamePattern.MatchString(name) {
		return "", Invalid("A name is up to 64 letters, digits, dots, dashes and underscores, starting with a letter or digit.")
	}
	return name, nil
}

const adminColumns = `id, name, created_at`

func scanAdmin(row interface{ Scan(...any) error }) (*Admin, error) {
	var (
		a       Admin
		created int64
	)
	if err := row.Scan(&a.ID, &a.Name, &created); err != nil {
		return nil, err
	}
	a.CreatedAt = fromUnix(created)
	return &a, nil
}

func adminWhere(ctx context.Context, db queryer, where string, args ...any) (*Admin, error) {
	a, err := scanAdmin(db.QueryRowContext(ctx, `SELECT `+adminColumns+` FROM admins WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound("There is no such admin.")
	}
	if err != nil {
		return nil, fmt.Errorf("admin: %w", err)
	}
	return a, nil
}

// CreateAdmin adds somebody who may sign in to the admin interface.
func (s *Store) CreateAdmin(ctx context.Context, name, password string) (*Admin, error) {
	name, err := AdminName(name)
	if err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	a := &Admin{ID: ids.New(ids.Admin, now.UnixMilli()), Name: name, CreatedAt: now}
	if err := insertAdmin(ctx, s.writer, a, hash); err != nil {
		return nil, err
	}
	s.changed()
	return a, nil
}

func insertAdmin(ctx context.Context, db execer, a *Admin, hash string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO admins (id, name, password_hash, created_at) VALUES (?, ?, ?, ?)`,
		a.ID, a.Name, hash, unix(a.CreatedAt))
	if isUnique(err) {
		return Conflict("There is already an admin called %s.", a.Name)
	}
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	return nil
}

// AuthenticateAdmin checks an admin's password, with the same refusal and the same time for a
// wrong password and a missing name, so latency is not a list of which names exist.
func (s *Store) AuthenticateAdmin(ctx context.Context, name, password string) (*Admin, error) {
	name, err := AdminName(name)
	if err != nil {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	var (
		hash    string
		a       Admin
		created int64
	)
	err = s.reader.QueryRowContext(ctx,
		`SELECT password_hash, `+adminColumns+` FROM admins WHERE name = ?`, name).
		Scan(&hash, &a.ID, &a.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		bcrypt.CompareHashAndPassword(decoyHash, []byte(password))
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("authenticate admin: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrNotFound
	}
	a.CreatedAt = fromUnix(created)
	return &a, nil
}

// AdminByID returns one admin.
func (s *Store) AdminByID(ctx context.Context, id string) (*Admin, error) {
	return adminWhere(ctx, s.reader, "id = ?", id)
}

// Admins returns every admin, by name.
func (s *Store) Admins(ctx context.Context) ([]Admin, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+adminColumns+` FROM admins ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("admins: %w", err)
	}
	defer rows.Close()
	var out []Admin
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, fmt.Errorf("admins: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// HasAdmin reports whether anybody can sign in to the admin interface. Nothing else decides
// whether serve prints an admin link at startup.
func (s *Store) HasAdmin(ctx context.Context) (bool, error) {
	var n int
	err := s.reader.QueryRowContext(ctx, `SELECT count(*) FROM admins`).Scan(&n)
	return n > 0, err
}

// SetAdminPassword replaces an admin's password and signs them out everywhere but keep, the
// session making the change, which is what somebody changing a password they think was seen
// wants. keep is a session cookie, or empty.
func (s *Store) SetAdminPassword(ctx context.Context, id, password, keep string) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set admin password: %w", err)
	}
	defer tx.Rollback()
	if err := setAdminPassword(ctx, tx, id, hash, keep); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set admin password: %w", err)
	}
	s.changed()
	return nil
}

func setAdminPassword(ctx context.Context, tx *sql.Tx, id, hash, keep string) error {
	res, err := tx.ExecContext(ctx, `UPDATE admins SET password_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return fmt.Errorf("set admin password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("There is no such admin.")
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM sessions WHERE admin_id = ? AND id_hash <> ?`, id, hashToken(keep)); err != nil {
		return fmt.Errorf("set admin password: %w", err)
	}
	return nil
}

// DeleteAdmin removes an admin and their sessions.
//
// The last admin stays. `mailio admin reset` would let somebody back in, but a click should not
// be what makes that necessary.
func (s *Store) DeleteAdmin(ctx context.Context, id string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete admin: %w", err)
	}
	defer tx.Rollback()

	if _, err := adminWhere(ctx, tx, "id = ?", id); err != nil {
		return err
	}
	var others int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM admins WHERE id <> ?`, id).Scan(&others); err != nil {
		return fmt.Errorf("delete admin: %w", err)
	}
	if others == 0 {
		return Conflict("That is the only admin. Add another first.")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM admins WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete admin: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete admin: %w", err)
	}
	s.changed()
	return nil
}
