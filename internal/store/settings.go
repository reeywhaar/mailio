package store

import (
	"context"
	"fmt"
)

// DMARC is what receivers are asked to do with mail forging a domain: mail whose From is the
// domain and which carries no signature of the domain's.
type DMARC string

const (
	DMARCReject     DMARC = "reject"
	DMARCQuarantine DMARC = "quarantine"
	DMARCNone       DMARC = "none"
)

func (p DMARC) valid() bool { return p == DMARCReject || p == DMARCQuarantine || p == DMARCNone }

// SPF is how a domain's SPF ends: what receivers are told about mail from servers it does not
// name.
type SPF string

const (
	// SPFFail says such mail is not the domain's.
	SPFFail SPF = "-all"
	// SPFSoftFail says it is probably not, the usual choice.
	SPFSoftFail SPF = "~all"
	// SPFNeutral says nothing either way.
	SPFNeutral SPF = "?all"
)

func (p SPF) valid() bool { return p == SPFFail || p == SPFSoftFail || p == SPFNeutral }

// Settings are the server-wide defaults a domain follows unless it has its own.
type Settings struct {
	// Unknown and UnknownAccountID are what a domain does with mail for addresses nobody has.
	Unknown          Unknown
	UnknownAccountID string
	DMARC            DMARC
	SPF              SPF
	// RelayID is the default relay, or empty for built in: each recipient's MX, directly.
	RelayID string
}

// Settings reads the defaults.
func (s *Store) Settings(ctx context.Context) (*Settings, error) {
	var set Settings
	if err := s.reader.QueryRowContext(ctx,
		`SELECT unknown, coalesce(unknown_account_id, ''), dmarc, spf, coalesce(relay_id, '') FROM settings WHERE id = 1`).
		Scan(&set.Unknown, &set.UnknownAccountID, &set.DMARC, &set.SPF, &set.RelayID); err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	return &set, nil
}

// SetSettings writes the defaults.
func (s *Store) SetSettings(ctx context.Context, set Settings) error {
	if !set.DMARC.valid() {
		return Invalid("A DMARC policy is reject, quarantine or none.")
	}
	if !set.SPF.valid() {
		return Invalid("An SPF ends in -all, ~all or ?all.")
	}
	junkTo, err := s.checkUnknown(ctx, set.Unknown, set.UnknownAccountID)
	if err != nil {
		return err
	}
	var relay any
	if set.RelayID != "" {
		if _, err := s.RelayByID(ctx, set.RelayID); err != nil {
			return err
		}
		relay = set.RelayID
	}
	res, err := s.writer.ExecContext(ctx,
		`UPDATE settings SET unknown = ?, unknown_account_id = ?, dmarc = ?, spf = ?, relay_id = ?
		  WHERE id = 1 AND (unknown <> ? OR unknown_account_id IS NOT ? OR dmarc <> ? OR spf <> ? OR relay_id IS NOT ?)`,
		set.Unknown, junkTo, set.DMARC, set.SPF, relay, set.Unknown, junkTo, set.DMARC, set.SPF, relay)
	if err != nil {
		return fmt.Errorf("set settings: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
	}
	return nil
}
