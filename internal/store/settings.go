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
	// Unknown is the catch-all, what a domain does with mail for addresses nobody has: reject or
	// drop. Forward and junk go to one account, so only a domain chooses them.
	Unknown Unknown
	DMARC   DMARC
	SPF     SPF
	// RelayID is the default relay, or empty for built-in: each recipient's MX, directly.
	RelayID string
	// MaxMessageBytes is the largest message an account takes, sends or stores, unless its domain
	// or the account itself says otherwise.
	MaxMessageBytes int64
}

// checkLimit refuses a largest message too small to be one or too large to hold.
func checkLimit(n int64) error {
	if n < 1<<20 || n > MessageCeiling {
		return Invalid("The largest message is from 1 MB to %s.", Megabytes(MessageCeiling))
	}
	return nil
}

// Settings reads the defaults.
func (s *Store) Settings(ctx context.Context) (*Settings, error) {
	var set Settings
	if err := s.reader.QueryRowContext(ctx,
		`SELECT unknown, dmarc, spf, coalesce(relay_id, ''), max_message_bytes FROM settings WHERE id = 1`).
		Scan(&set.Unknown, &set.DMARC, &set.SPF, &set.RelayID, &set.MaxMessageBytes); err != nil {
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
	if set.Unknown != UnknownReject && set.Unknown != UnknownDrop {
		return Invalid("The default catch-all rejects or drops. Forward and junk go to an account, chosen per domain.")
	}
	if err := checkLimit(set.MaxMessageBytes); err != nil {
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
		`UPDATE settings SET unknown = ?, dmarc = ?, spf = ?, relay_id = ?, max_message_bytes = ?
		  WHERE id = 1 AND (unknown <> ? OR dmarc <> ? OR spf <> ? OR relay_id IS NOT ? OR max_message_bytes <> ?)`,
		set.Unknown, set.DMARC, set.SPF, relay, set.MaxMessageBytes, set.Unknown, set.DMARC, set.SPF, relay, set.MaxMessageBytes)
	if err != nil {
		return fmt.Errorf("set settings: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.changed()
	}
	return nil
}
