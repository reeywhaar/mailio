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
		r.User = u
		return r, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	switch d.Unknown {
	case UnknownDrop:
		r.Drop = true
	case UnknownJunk:
		if r.User, err = s.UserByID(ctx, d.UnknownUserID); err != nil {
			return nil, err
		}
		r.Junk = true
	}
	return r, nil
}
