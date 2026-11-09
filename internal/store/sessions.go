package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// How long a session lasts and how often its window is pushed forward.
//
// The throttle is not an optimization: without it every request rewrites the row and emits a
// Set-Cookie, for a window measured in days.
const (
	SessionLifetime = 7 * 24 * time.Hour
	SessionRefresh  = time.Hour
)

const userAgentMax = 400

// Session is one live sign-in to the admin interface.
//
// There is no field holding the cookie value. The table is keyed by sha256 of it, so nothing
// readable ever contains something replayable.
type Session struct {
	UserID    string
	ExpiresAt time.Time
}

// CreateSession records a sign-in. token is the cookie value; only its hash is stored.
func (s *Store) CreateSession(ctx context.Context, token, userID, userAgent string) (*Session, error) {
	now := s.Now()
	expires := now.Add(SessionLifetime)
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, user_id, user_agent, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		hashToken(token), userID, truncate(userAgent, userAgentMax), unix(now), unix(now), unix(expires))
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &Session{UserID: userID, ExpiresAt: expires}, nil
}

// SessionByToken returns a live session, or ErrNotFound. Expiry is applied in the query: a
// lapsed row is not a session.
func (s *Store) SessionByToken(ctx context.Context, token string) (*Session, error) {
	var (
		sess    Session
		expires int64
	)
	err := s.reader.QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM sessions WHERE id_hash = ? AND expires_at > ?`,
		hashToken(token), unix(s.Now())).Scan(&sess.UserID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	sess.ExpiresAt = fromUnix(expires)
	return &sess, nil
}

// TouchSession slides the window forward and reports whether it moved.
func (s *Store) TouchSession(ctx context.Context, token, userAgent string) (time.Time, bool, error) {
	now := s.Now()
	expires := now.Add(SessionLifetime)
	res, err := s.writer.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ?, expires_at = ?, user_agent = ?
		   WHERE id_hash = ? AND last_seen_at <= ?`,
		unix(now), unix(expires), truncate(userAgent, userAgentMax),
		hashToken(token), unix(now.Add(-SessionRefresh)))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("touch session: %w", err)
	}
	n, _ := res.RowsAffected()
	return expires, n > 0, nil
}

// DeleteSession signs one session out. One already gone is not an error: a sign-out is a
// statement about the future.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.writer.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hashToken(token))
	return err
}

// SweepSessions collects lapsed rows, which are already unusable.
func (s *Store) SweepSessions(ctx context.Context) (int64, error) {
	res, err := s.writer.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, unix(s.Now()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
