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

	"mailio/internal/address"
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

// UseAdminLink spends a link on an address and a password, and returns the admin it made.
//
// An address nobody has becomes a new user, and its domain is added if mailio did not receive
// for it yet, which is how the first domain and the first user come to exist. An address that
// already exists is given the password and made an admin, signed out everywhere it was signed in.
//
// The address and password are checked before the link is touched, so a typo leaves it unspent.
// The spend is a guarded delete in the same transaction as the user, so two requests racing on
// one link make one admin.
func (s *Store) UseAdminLink(ctx context.Context, token, addr, password string) (*User, error) {
	a, err := address.ParseUser(addr)
	if err != nil {
		return nil, Invalid("%s", err)
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

	d, err := domainWhere(ctx, tx, "name = ?", a.Domain)
	if errors.Is(err, ErrNotFound) {
		d, err = s.newDomain(a.Domain)
		if err == nil {
			err = insertDomain(ctx, tx, d)
		}
	}
	if err != nil {
		return nil, err
	}

	u, err := userWhere(ctx, tx, "u.domain_id = ? AND u.local_part = ?", d.ID, a.Local)
	switch {
	case err == nil:
		if err := setPassword(ctx, tx, u.ID, hash); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET admin = 1 WHERE id = ?`, u.ID); err != nil {
			return nil, fmt.Errorf("use admin link: %w", err)
		}
		u.Admin = true
	case errors.Is(err, ErrNotFound):
		u = &User{ID: ids.New(ids.User, now.UnixMilli()), Local: a.Local, Domain: d.Name, DomainID: d.ID, Admin: true, CreatedAt: now}
		if err := insertUser(ctx, tx, u, hash); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("use admin link: %w", err)
	}
	return u, nil
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
