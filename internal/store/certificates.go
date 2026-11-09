package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Certificate is a TLS certificate for the mail ports, as PEM.
type Certificate struct {
	Host     string
	Chain    string
	Key      string
	Issuer   string
	NotAfter time.Time
}

// CertificateFor returns the stored certificate for a host, or ErrNotFound.
func (s *Store) CertificateFor(ctx context.Context, host string) (*Certificate, error) {
	var (
		c        Certificate
		notAfter int64
	)
	err := s.reader.QueryRowContext(ctx,
		`SELECT host, chain, key, issuer, not_after FROM certificates WHERE host = ?`, host).
		Scan(&c.Host, &c.Chain, &c.Key, &c.Issuer, &notAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	c.NotAfter = fromUnix(notAfter)
	return &c, nil
}

// SaveCertificate replaces a host's certificate.
func (s *Store) SaveCertificate(ctx context.Context, c *Certificate) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO certificates (host, chain, key, issuer, not_after, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (host) DO UPDATE SET chain = excluded.chain, key = excluded.key, issuer = excluded.issuer,
		   not_after = excluded.not_after, updated_at = excluded.updated_at`,
		c.Host, c.Chain, c.Key, c.Issuer, unix(c.NotAfter), unix(s.Now()))
	if err != nil {
		return fmt.Errorf("save certificate: %w", err)
	}
	s.changed()
	return nil
}

// ACMEAccountKey returns the stored account key, or ErrNotFound.
func (s *Store) ACMEAccountKey(ctx context.Context) (string, error) {
	var key string
	err := s.reader.QueryRowContext(ctx, `SELECT key FROM acme_account WHERE id = 1`).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return key, err
}

// SaveACMEAccountKey stores the account key.
func (s *Store) SaveACMEAccountKey(ctx context.Context, key string) error {
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO acme_account (id, key) VALUES (1, ?) ON CONFLICT (id) DO UPDATE SET key = excluded.key`, key)
	if err != nil {
		return fmt.Errorf("save acme account: %w", err)
	}
	s.changed()
	return nil
}
