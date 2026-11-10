package store

import (
	"context"
	"errors"

	"mailio/internal/address"
)

// Recipient is where mail to one address goes. See docs/addresses.md.
type Recipient struct {
	// Ours is whether mailio receives for the domain at all. Mail to any other domain is
	// relaying, which only a signed-in account may ask for.
	Ours bool
	// Account gets the mail. Nil for an address at our domain that nobody has, unless the domain
	// sends that mail to somebody as junk.
	Account *Account
	// Junk is mail for an address nobody has, for the Junk folder of the account the domain's
	// catch-all chose. Forwarded, it goes to that account's INBOX like any other.
	Junk bool
	// Drop is mail accepted and kept nowhere.
	Drop bool
	// Refused is an account who takes no mail, or forwards it to one who does not.
	Refused bool
	// Tag is what followed the + in the address.
	Tag string
	// Limit is the largest message it takes: the smallest of the address's own, its domain's for
	// an address nobody has, and that of each account the mail is sent on to.
	Limit int64
}

// Resolve decides where mail to a goes: the account registered under its name, or failing that what
// its domain does with unknown mail.
func (s *Store) Resolve(ctx context.Context, a address.Address) (*Recipient, error) {
	d, err := s.DomainByName(ctx, a.Domain)
	if errors.Is(err, ErrNotFound) {
		return &Recipient{}, nil
	}
	if err != nil {
		return nil, err
	}

	r := &Recipient{Ours: true, Tag: a.Tag()}
	u, err := accountWhere(ctx, s.reader, "u.domain_id = ? AND u.local_part = ?", d.ID, a.Name())
	if err == nil {
		r.Limit = u.MessageLimit
		return r, s.follow(ctx, r, u)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	set, err := s.Settings(ctx)
	if err != nil {
		return nil, err
	}
	r.Limit = d.MessageLimit(set)
	unknown, to := d.UnknownFor(set)
	switch unknown {
	case UnknownDrop:
		r.Drop = true
	case UnknownForward, UnknownJunk:
		u, err := s.AccountByID(ctx, to)
		if err != nil {
			return nil, err
		}
		r.Junk = unknown == UnknownJunk
		return r, s.follow(ctx, r, u)
	}
	return r, nil
}

// follow settles which account gets mail meant for u: u itself, whoever it forwards to, or nobody
// when one of them refuses mail.
func (s *Store) follow(ctx context.Context, r *Recipient, u *Account) error {
	for hops := 0; ; hops++ {
		r.Limit = min(r.Limit, u.MessageLimit)
		switch {
		case u.Incoming == IncomingReject || hops > maxForwards:
			r.Account, r.Refused = nil, true
			return nil
		case u.Incoming == IncomingForward:
			next, err := s.AccountByID(ctx, u.ForwardAccountID)
			if err != nil {
				return err
			}
			u = next
		default:
			r.Account = u
			return nil
		}
	}
}
