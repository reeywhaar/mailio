package store

import (
	"context"
	"errors"

	"mailio/internal/address"
)

// Recipient is where mail to one address goes. See docs/addresses.md.
type Recipient struct {
	// Ours is whether mailio receives for the domain at all. Mail to any other domain is
	// relaying, which only a signed-in user may ask for.
	Ours bool
	// User gets the mail. Nil for an address at our domain that nobody has, unless the domain
	// sends that mail to somebody as junk.
	User *User
	// Junk is mail for an address nobody has, delivered to the domain's chosen user.
	Junk bool
	// Drop is mail accepted and kept nowhere.
	Drop bool
	// Refused is a user who takes no mail, or forwards it to one who does not.
	Refused bool
	// Tag is what followed the + in the address.
	Tag string
}

// Resolve decides where mail to a goes: the user registered under its name, or failing that what
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
	u, err := userWhere(ctx, s.reader, "u.domain_id = ? AND u.local_part = ?", d.ID, a.Name())
	if err == nil {
		return r, s.follow(ctx, r, u)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	switch d.Unknown {
	case UnknownDrop:
		r.Drop = true
	case UnknownJunk:
		u, err := s.UserByID(ctx, d.UnknownUserID)
		if err != nil {
			return nil, err
		}
		r.Junk = true
		return r, s.follow(ctx, r, u)
	}
	return r, nil
}

// follow settles which user gets mail meant for u: u itself, whoever it forwards to, or nobody
// when one of them refuses mail.
func (s *Store) follow(ctx context.Context, r *Recipient, u *User) error {
	for hops := 0; ; hops++ {
		switch {
		case u.Incoming == IncomingReject || hops > maxForwards:
			r.User, r.Refused = nil, true
			return nil
		case u.Incoming == IncomingForward:
			next, err := s.UserByID(ctx, u.ForwardUserID)
			if err != nil {
				return err
			}
			u = next
		default:
			r.User = u
			return nil
		}
	}
}
