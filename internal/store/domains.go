package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mailio/internal/address"
	"mailio/internal/dkimkey"
	"mailio/internal/ids"
)

// Unknown is a domain's catch-all: what it does with mail for an address nobody has.
type Unknown string

const (
	// UnknownReject refuses it while the sender is still connected.
	UnknownReject Unknown = "reject"
	// UnknownDrop accepts it and keeps nothing.
	UnknownDrop Unknown = "drop"
	// UnknownForward delivers it to one chosen account's INBOX.
	UnknownForward Unknown = "forward"
	// UnknownJunk delivers it to one chosen account's Junk.
	UnknownJunk Unknown = "junk"
)

func (u Unknown) valid() bool {
	return u == UnknownReject || u == UnknownDrop || u == UnknownForward || u == UnknownJunk
}

// toAccount is whether it goes to an account, which only a domain can choose.
func (u Unknown) toAccount() bool { return u == UnknownForward || u == UnknownJunk }

// Domain is one domain mailio receives mail for.
type Domain struct {
	ID      string
	Name    string
	Unknown Unknown
	// UnknownAccountID is who gets the mail, set exactly when Unknown is UnknownForward or
	// UnknownJunk. Both are the domain's own when UnknownOwn is set, and ignored for the default's
	// otherwise.
	UnknownAccountID string
	UnknownOwn       bool
	CreatedAt        time.Time
	// DKIMSelector and DKIMKey sign its outbound mail; empty until mailio makes the key.
	DKIMSelector string
	DKIMKey      string
	// DMARC and SPF are the domain's own, or empty to follow the defaults in Settings.
	DMARC DMARC
	SPF   SPF
	// Via is how its mail goes out: empty follows the default, ViaBuiltIn delivers directly, and
	// ViaRelay goes through RelayID.
	Via     string
	RelayID string
	// MaxMessageBytes is the domain's own largest message, or 0 to follow the default.
	MaxMessageBytes int64
	// TrashDays and JunkDays are how long its accounts' Trash and Junk keep a message, 0 forever,
	// or nil to follow the default.
	TrashDays *int
	JunkDays  *int
}

// UnknownFor is what the domain does with mail for addresses nobody has: its own, or the
// default's.
func (d *Domain) UnknownFor(set *Settings) (Unknown, string) {
	if d.UnknownOwn {
		return d.Unknown, d.UnknownAccountID
	}
	return set.Unknown, ""
}

// MessageLimit is the largest message the domain's addresses take: its own, or the default's.
func (d *Domain) MessageLimit(set *Settings) int64 {
	if d.MaxMessageBytes > 0 {
		return d.MaxMessageBytes
	}
	return set.MaxMessageBytes
}

// Policies are what mailio publishes for the domain: its own, or the defaults.
func (d *Domain) Policies(set *Settings) (DMARC, SPF) {
	dmarc, spf := set.DMARC, set.SPF
	if d.DMARC != "" {
		dmarc = d.DMARC
	}
	if d.SPF != "" {
		spf = d.SPF
	}
	return dmarc, spf
}

const domainColumns = `id, name, unknown, coalesce(unknown_account_id, ''), unknown_own, created_at, dkim_selector, dkim_key,
	coalesce(dmarc, ''), coalesce(spf, ''), coalesce(send_via, ''), coalesce(relay_id, ''), coalesce(max_message_bytes, 0), trash_days, junk_days`

func scanDomain(row interface{ Scan(...any) error }) (*Domain, error) {
	var (
		d       Domain
		created int64
	)
	if err := row.Scan(&d.ID, &d.Name, &d.Unknown, &d.UnknownAccountID, &d.UnknownOwn, &created, &d.DKIMSelector, &d.DKIMKey, &d.DMARC, &d.SPF, &d.Via, &d.RelayID, &d.MaxMessageBytes, &d.TrashDays, &d.JunkDays); err != nil {
		return nil, err
	}
	d.CreatedAt = fromUnix(created)
	return &d, nil
}

// CreateDomain starts receiving for a domain. Mail to it is refused until an account is made there.
func (s *Store) CreateDomain(ctx context.Context, name string) (*Domain, error) {
	d, err := s.newDomain(name)
	if err != nil {
		return nil, err
	}
	if err := insertDomain(ctx, s.writer, d); err != nil {
		return nil, err
	}
	s.changed()
	return d, nil
}

func (s *Store) newDomain(name string) (*Domain, error) {
	a, err := address.Parse("x@" + name)
	if err != nil {
		return nil, Invalid("%s", err)
	}
	key, err := dkimkey.Generate()
	if err != nil {
		return nil, err
	}
	now := s.Now()
	return &Domain{ID: ids.New(ids.Domain, now.UnixMilli()), Name: a.Domain, Unknown: UnknownReject, CreatedAt: now,
		DKIMSelector: dkimkey.Selector, DKIMKey: key}, nil
}

func insertDomain(ctx context.Context, db execer, d *Domain) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO domains (id, name, unknown, created_at, dkim_selector, dkim_key) VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.Name, d.Unknown, unix(d.CreatedAt), d.DKIMSelector, d.DKIMKey)
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

// checkUnknown validates a catch-all: accountID names who gets the mail for UnknownForward and
// UnknownJunk, and is empty otherwise. The account may be at any domain: one domain's catch-all
// can be read by somebody at another.
func checkUnknown(ctx context.Context, q queryer, u Unknown, accountID string) (any, error) {
	if !u.valid() {
		return nil, Invalid("A catch-all rejects, drops, forwards, or delivers as junk.")
	}
	if u.toAccount() != (accountID != "") {
		if u.toAccount() {
			return nil, Invalid("Choose who gets the mail.")
		}
		return nil, Invalid("Only forwarded mail and junk go to an account.")
	}
	if accountID == "" {
		return nil, nil
	}
	a, err := accountWhere(ctx, q, "u.id = ?", accountID)
	if err != nil {
		return nil, err
	}
	if err := keeps(a, "catch-all mail"); err != nil {
		return nil, err
	}
	return accountID, nil
}

// SetUnknown sets the domain's catch-all, what it does with mail for addresses nobody has; ""
// follows the default. accountID names who gets the mail for UnknownForward and UnknownJunk, and
// is empty otherwise.
func (s *Store) SetUnknown(ctx context.Context, domainID string, u Unknown, accountID string) error {
	own := u != ""
	if !own {
		u, accountID = UnknownReject, ""
	}
	// Checked in the transaction that writes, as SetIncoming checks the other side.
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set unknown: %w", err)
	}
	defer tx.Rollback()
	target, err := checkUnknown(ctx, tx, u, accountID)
	if err != nil {
		return err
	}
	// The comparison is what makes saving the same choice no change, and nothing for the backup
	// loop to count.
	res, err := tx.ExecContext(ctx,
		`UPDATE domains SET unknown = ?, unknown_account_id = ?, unknown_own = ?
		  WHERE id = ? AND (unknown <> ? OR unknown_account_id IS NOT ? OR unknown_own <> ?)`,
		u, target, own, domainID, u, target, own)
	if err != nil {
		return fmt.Errorf("set unknown: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set unknown: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
		return nil
	}
	_, err = s.DomainByID(ctx, domainID)
	return err
}

// SetDMARC sets a domain's own DMARC policy, or with "" returns it to the default.
func (s *Store) SetDMARC(ctx context.Context, domainID string, policy DMARC) error {
	if policy != "" && !policy.valid() {
		return Invalid("A DMARC policy is reject, quarantine or none.")
	}
	return s.setDomainColumn(ctx, domainID, "dmarc", orNull(policy))
}

// SetSPF sets how a domain's own SPF ends, or with "" returns it to the default.
func (s *Store) SetSPF(ctx context.Context, domainID string, spf SPF) error {
	if spf != "" && !spf.valid() {
		return Invalid("An SPF ends in -all, ~all or ?all.")
	}
	return s.setDomainColumn(ctx, domainID, "spf", orNull(spf))
}

// SetDomainMessageLimit sets a domain's own largest message, or with 0 returns it to the default.
func (s *Store) SetDomainMessageLimit(ctx context.Context, domainID string, n int64) error {
	if n != 0 {
		if err := checkLimit(n); err != nil {
			return err
		}
	}
	return s.setDomainColumn(ctx, domainID, "max_message_bytes", orNull(n))
}

// SetDomainKept sets how long a domain's accounts' Trash or Junk keeps a message, 0 forever, or
// with nil returns it to the default.
func (s *Store) SetDomainKept(ctx context.Context, domainID string, k Kept, days *int) error {
	v, err := k.value(days)
	if err != nil {
		return err
	}
	return s.setDomainColumn(ctx, domainID, k.column, v)
}

// orNull is a value as an override column holds it: null for the zero value, which follows the
// default.
func orNull[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

// setDomainColumn writes one of the override columns, v nil for null. column is a constant of the
// callers', never input.
func (s *Store) setDomainColumn(ctx context.Context, domainID, column string, v any) error {
	res, err := s.writer.ExecContext(ctx,
		`UPDATE domains SET `+column+` = ? WHERE id = ? AND `+column+` IS NOT ?`, v, domainID, v)
	if err != nil {
		return fmt.Errorf("set %s: %w", column, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
		return nil
	}
	_, err = s.DomainByID(ctx, domainID)
	return err
}

// DeleteDomain stops receiving for a domain. Its accounts go first, so deleting a domain never
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
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts WHERE domain_id = ?`, id).Scan(&n); err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	if n > 0 {
		return Conflict("%s still has accounts. Delete them first.", d.Name)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM domains WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	s.changed()
	return nil
}

// EnsureDKIM gives every domain without a signing key one. A domain from before mailio signed is
// the only kind without.
func (s *Store) EnsureDKIM(ctx context.Context) error {
	domains, err := s.Domains(ctx)
	if err != nil {
		return err
	}
	for _, d := range domains {
		if d.DKIMKey != "" {
			continue
		}
		key, err := dkimkey.Generate()
		if err != nil {
			return err
		}
		if err := s.SetDKIM(ctx, d.ID, dkimkey.Selector, key); err != nil {
			return err
		}
	}
	return nil
}
