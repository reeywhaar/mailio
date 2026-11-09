package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"mailio/internal/ids"
)

// AdminLinkLifetime is how long an admin link is good for. Single use either way.
const AdminLinkLifetime = 24 * time.Hour

// CreateAdminLink mints the one live admin link and returns its token, which is readable exactly
// once. Any earlier link stops working.
//
// The table holds the token's hash and never the token, so a lost link is reissued rather than
// recovered.
func (s *Store) CreateAdminLink(ctx context.Context) (token string, expires time.Time, err error) {
	token, err = Secret()
	if err != nil {
		return "", time.Time{}, err
	}
	now := s.Now()
	expires = now.Add(AdminLinkLifetime)

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create admin link: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM admin_links`); err != nil {
		return "", time.Time{}, fmt.Errorf("create admin link: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO admin_links (token_hash, created_at, expires_at) VALUES (?, ?, ?)`,
		hashToken(token), unix(now), unix(expires)); err != nil {
		return "", time.Time{}, fmt.Errorf("create admin link: %w", err)
	}
	return token, expires, tx.Commit()
}

// errLinkDead is the one refusal for a link that was used, replaced, expired, or never existed:
// telling them apart tells a stranger which tokens were real.
var errLinkDead = NotFound("That link has been used or has expired. Run `mailio admin reset` for a new one.")

// AdminLinkExpiry returns when a live link expires. It reveals nothing but the link's own
// validity, which lets the page say a link is dead before somebody types a password into it.
func (s *Store) AdminLinkExpiry(ctx context.Context, token string) (time.Time, error) {
	var expires int64
	err := s.reader.QueryRowContext(ctx,
		`SELECT expires_at FROM admin_links WHERE token_hash = ? AND expires_at > ?`,
		hashToken(token), unix(s.Now())).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, errLinkDead
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("admin link: %w", err)
	}
	return fromUnix(expires), nil
}

// UseAdminLink spends a link on a name and a password, and returns the admin it made.
//
// A name nobody has becomes a new admin, which is how the first one comes to exist. A name that
// already exists is given the password and signed out everywhere.
//
// The name and password are checked before the link is touched, so a typo leaves it unspent. The
// spend is a guarded delete in the same transaction as the admin, so two requests racing on one
// link make one admin.
func (s *Store) UseAdminLink(ctx context.Context, token, name, password string) (*Admin, error) {
	name, err := AdminName(name)
	if err != nil {
		return nil, err
	}
	if _, err := s.AdminLinkExpiry(ctx, token); err != nil {
		return nil, err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, err
	}

	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("use admin link: %w", err)
	}
	defer tx.Rollback()

	now := s.Now()
	res, err := tx.ExecContext(ctx,
		`DELETE FROM admin_links WHERE token_hash = ? AND expires_at > ?`, hashToken(token), unix(now))
	if err != nil {
		return nil, fmt.Errorf("use admin link: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errLinkDead
	}

	a, err := adminWhere(ctx, tx, "name = ?", name)
	switch {
	case err == nil:
		if err := setAdminPassword(ctx, tx, a.ID, hash, ""); err != nil {
			return nil, err
		}
	case errors.Is(err, ErrNotFound):
		a = &Admin{ID: ids.New(ids.Admin, now.UnixMilli()), Name: name, CreatedAt: now}
		if err := insertAdmin(ctx, tx, a, hash); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("use admin link: %w", err)
	}
	s.changed()
	return a, nil
}

// SweepAdminLinks collects a link nobody used.
func (s *Store) SweepAdminLinks(ctx context.Context) (int64, error) {
	res, err := s.writer.ExecContext(ctx, `DELETE FROM admin_links WHERE expires_at <= ?`, unix(s.Now()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Secret is 32 random bytes as base64url: a session cookie, an admin link's token.
func Secret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
