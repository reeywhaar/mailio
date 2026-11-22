package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mailio/internal/ids"
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
// readable ever contains something replayable, and ID is a hash of that again: what a list shows
// and a sign-out names.
type Session struct {
	ID         string
	AdminID    string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// SessionID is the ID of the session a cookie keys.
func SessionID(token string) string { return sessionID(hashToken(token)) }

func sessionID(idHash []byte) string { return ids.Derive(ids.Session, idHash) }

// CreateSession records a sign-in. token is the cookie value; only its hash is stored.
func (s *Store) CreateSession(ctx context.Context, token, adminID, userAgent string) (*Session, error) {
	now := s.Now()
	expires := now.Add(SessionLifetime)
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO sessions (id_hash, admin_id, user_agent, created_at, last_seen_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		hashToken(token), adminID, truncate(userAgent, userAgentMax), unix(now), unix(now), unix(expires))
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &Session{AdminID: adminID, ExpiresAt: expires}, nil
}

// SessionByToken returns a live session, or ErrNotFound. Expiry is applied in the query: a
// lapsed row is not a session.
func (s *Store) SessionByToken(ctx context.Context, token string) (*Session, error) {
	var (
		sess    Session
		expires int64
	)
	err := s.reader.QueryRowContext(ctx,
		`SELECT admin_id, expires_at FROM sessions WHERE id_hash = ? AND expires_at > ?`,
		hashToken(token), unix(s.Now())).Scan(&sess.AdminID, &expires)
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

// Sessions are an admin's live sign-ins, the one used last first.
func (s *Store) Sessions(ctx context.Context, adminID string) ([]Session, error) {
	rows, err := s.reader.QueryContext(ctx,
		`SELECT id_hash, user_agent, created_at, last_seen_at, expires_at
		   FROM sessions WHERE admin_id = ? AND expires_at > ?
		  ORDER BY last_seen_at DESC, created_at DESC`,
		adminID, unix(s.Now()))
	if err != nil {
		return nil, fmt.Errorf("sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var (
			idHash                     []byte
			created, lastSeen, expires int64
			sess                       = Session{AdminID: adminID}
		)
		if err := rows.Scan(&idHash, &sess.UserAgent, &created, &lastSeen, &expires); err != nil {
			return nil, fmt.Errorf("sessions: %w", err)
		}
		sess.ID = sessionID(idHash)
		sess.CreatedAt, sess.LastSeenAt, sess.ExpiresAt = fromUnix(created), fromUnix(lastSeen), fromUnix(expires)
		out = append(out, sess)
	}
	return out, rows.Err()
}

// RevokeSession signs out one of an admin's sessions, the one it is asked from too, by its ID.
// Only the admin's own are looked through, so another admin's ID is no such session.
func (s *Store) RevokeSession(ctx context.Context, adminID, id string) error {
	rows, err := s.reader.QueryContext(ctx, `SELECT id_hash FROM sessions WHERE admin_id = ?`, adminID)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	var target []byte
	for rows.Next() {
		var idHash []byte
		if err := rows.Scan(&idHash); err != nil {
			rows.Close()
			return fmt.Errorf("revoke session: %w", err)
		}
		if sessionID(idHash) == id {
			target = idHash
			break
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if target == nil {
		return NotFound("There is no such session.")
	}
	if _, err := s.writer.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, target); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeOtherSessions signs an admin out everywhere but keep, the cookie asking, and says how many.
func (s *Store) RevokeOtherSessions(ctx context.Context, adminID, keep string) (int64, error) {
	res, err := s.writer.ExecContext(ctx,
		`DELETE FROM sessions WHERE admin_id = ? AND id_hash <> ?`, adminID, hashToken(keep))
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	return res.RowsAffected()
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
