package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mailio/internal/address"
	"mailio/internal/ids"
)

// Unknown is what a domain does with mail for an address nobody has.
type Unknown string

const (
	// UnknownReject refuses it while the sender is still connected.
	UnknownReject Unknown = "reject"
	// UnknownDrop accepts it and keeps nothing.
	UnknownDrop Unknown = "drop"
	// UnknownJunk delivers it to one chosen user, as junk.
	UnknownJunk Unknown = "junk"
)

func (u Unknown) valid() bool {
	return u == UnknownReject || u == UnknownDrop || u == UnknownJunk
}

// Domain is one domain mailio receives mail for.
type Domain struct {
	ID      string
	Name    string
	Unknown Unknown
	// UnknownUserID is who gets the junk, set exactly when Unknown is UnknownJunk.
	UnknownUserID string
	CreatedAt     time.Time
}

const domainColumns = `id, name, unknown, coalesce(unknown_user_id, ''), created_at`

func scanDomain(row interface{ Scan(...any) error }) (*Domain, error) {
	var (
		d       Domain
		created int64
	)
	if err := row.Scan(&d.ID, &d.Name, &d.Unknown, &d.UnknownUserID, &created); err != nil {
		return nil, err
	}
	d.CreatedAt = fromUnix(created)
	return &d, nil
}

// CreateDomain starts receiving for a domain. Mail to it is refused until a user is made there.
func (s *Store) CreateDomain(ctx context.Context, name string) (*Domain, error) {
	d, err := s.newDomain(name)
	if err != nil {
		return nil, err
	}
	if err := insertDomain(ctx, s.writer, d); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) newDomain(name string) (*Domain, error) {
	a, err := address.Parse("x@" + name)
	if err != nil {
		return nil, Invalid("%s", err)
	}
	now := s.Now()
	return &Domain{ID: ids.New(ids.Domain, now.UnixMilli()), Name: a.Domain, Unknown: UnknownReject, CreatedAt: now}, nil
}

func insertDomain(ctx context.Context, db execer, d *Domain) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO domains (id, name, unknown, created_at) VALUES (?, ?, ?, ?)`,
		d.ID, d.Name, d.Unknown, unix(d.CreatedAt))
	if isUnique(err) {
		return Conflict("mailio already receives for %s.", d.Name)
	}
	if err != nil {
		return fmt.Errorf("create domain: %w", err)
	}
	return nil
}

// Domains returns every domain, by name.
func (s *Store) Domains(ctx context.Context) ([]Domain, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT `+domainColumns+` FROM domains ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("domains: %w", err)
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, fmt.Errorf("domains: %w", err)
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DomainByID returns one domain.
func (s *Store) DomainByID(ctx context.Context, id string) (*Domain, error) {
	return domainWhere(ctx, s.reader, "id = ?", id)
}

// DomainByName returns the domain mail to name is received for.
func (s *Store) DomainByName(ctx context.Context, name string) (*Domain, error) {
	return domainWhere(ctx, s.reader, "name = ?", name)
}

func domainWhere(ctx context.Context, db queryer, where string, args ...any) (*Domain, error) {
	d, err := scanDomain(db.QueryRowContext(ctx, `SELECT `+domainColumns+` FROM domains WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound("There is no such domain.")
	}
	if err != nil {
		return nil, fmt.Errorf("domain: %w", err)
	}
	return d, nil
}

// SetUnknown decides what the domain does with mail for addresses nobody has. userID names who
// gets it for UnknownJunk, and is empty otherwise.
//
// The user may be at any domain: junk for one domain can be read by somebody at another.
func (s *Store) SetUnknown(ctx context.Context, domainID string, u Unknown, userID string) error {
	if !u.valid() {
		return Invalid("Unknown mail is rejected, dropped, or delivered as junk.")
	}
	if (u == UnknownJunk) != (userID != "") {
		if u == UnknownJunk {
			return Invalid("Choose who gets the junk.")
		}
		return Invalid("Only junk goes to a user.")
	}
	var target any
	if userID != "" {
		if _, err := s.UserByID(ctx, userID); err != nil {
			return err
		}
		target = userID
	}
	res, err := s.writer.ExecContext(ctx,
		`UPDATE domains SET unknown = ?, unknown_user_id = ? WHERE id = ?`, u, target, domainID)
	if err != nil {
		return fmt.Errorf("set unknown: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NotFound("There is no such domain.")
	}
	return nil
}

// DeleteDomain stops receiving for a domain. Its users go first, so deleting a domain never
// deletes anybody's mail along with it.
func (s *Store) DeleteDomain(ctx context.Context, id string) error {
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	defer tx.Rollback()

	d, err := domainWhere(ctx, tx, "id = ?", id)
	if err != nil {
		return err
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE domain_id = ?`, id).Scan(&n); err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	if n > 0 {
		return Conflict("%s still has users. Delete them first.", d.Name)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	return tx.Commit()
}
