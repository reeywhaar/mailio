// Package address reads mail addresses and decides which part of one names an account.
//
// Everything is lowercased on the way in. Mail systems treat local parts as case-insensitive in
// practice, whatever RFC 5321 permits, and an address that reaches an account spelled one way and
// bounces spelled another is worse than two addresses that cannot be told apart.
package address

import (
	"errors"
	"strings"
)

// Separator divides the name an account is registered under from a tag: mail to name+shop@ is for
// name@. See docs/addresses.md.
const Separator = "+"

const (
	maxLocal  = 64
	maxDomain = 253
	maxLabel  = 63
)

// Address is a parsed, lowercased address.
type Address struct {
	Local  string
	Domain string
}

func (a Address) String() string { return a.Local + "@" + a.Domain }

// Name is the local part up to the separator: what an account is registered under.
func (a Address) Name() string {
	name, _, _ := strings.Cut(a.Local, Separator)
	return name
}

// Tag is what follows the separator, or nothing.
func (a Address) Tag() string {
	_, tag, _ := strings.Cut(a.Local, Separator)
	return tag
}

// Parse reads local@domain.
//
// Quoted local parts and address literals are refused: nobody can be registered under one, so
// mail to one has nowhere to go.
func Parse(s string) (Address, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	i := strings.LastIndexByte(s, '@')
	if i < 0 {
		return Address{}, errors.New("An address needs an @ in it.")
	}
	a := Address{Local: s[:i], Domain: strings.TrimSuffix(s[i+1:], ".")}
	if err := checkLocal(a.Local); err != nil {
		return Address{}, err
	}
	if err := CheckDomain(a.Domain); err != nil {
		return Address{}, err
	}
	return a, nil
}

// ParseAccount reads an address somebody wants to register an account under.
func ParseAccount(s string) (Address, error) {
	a, err := Parse(s)
	if err != nil {
		return Address{}, err
	}
	if strings.Contains(a.Local, Separator) {
		return Address{}, errors.New("An account's address cannot have a + in it: mail to name+anything already reaches name.")
	}
	return a, nil
}

// checkLocal admits a dot-atom of ASCII atext, which is every address anybody registers.
func checkLocal(local string) error {
	switch {
	case local == "":
		return errors.New("An address needs something before the @.")
	case len(local) > maxLocal:
		return errors.New("The part before the @ is longer than 64 characters.")
	case local[0] == '.' || local[len(local)-1] == '.' || strings.Contains(local, ".."):
		return errors.New("The part before the @ cannot start or end with a dot, or have two in a row.")
	}
	for i := 0; i < len(local); i++ {
		if !atext(local[i]) && local[i] != '.' {
			return errors.New("The part before the @ has a character an address cannot have.")
		}
	}
	return nil
}

func atext(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("!#$%&'*+-/=?^_`{|}~", c) >= 0
}

// CheckDomain admits a lowercased hostname of at least two labels whose last is not all digits,
// so an IP address is not mistaken for a domain.
func CheckDomain(domain string) error {
	switch {
	case domain == "":
		return errors.New("An address needs a domain after the @.")
	case len(domain) > maxDomain:
		return errors.New("That domain is longer than 253 characters.")
	case !strings.Contains(domain, "."):
		return errors.New("That domain needs a dot in it, like example.com.")
	}
	labels := strings.Split(domain, ".")
	for _, l := range labels {
		if l == "" || len(l) > maxLabel || l[0] == '-' || l[len(l)-1] == '-' {
			return errors.New("That is not a domain name.")
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("That domain has a character a domain cannot have.")
			}
		}
	}
	if strings.Trim(labels[len(labels)-1], "0123456789") == "" {
		return errors.New("That is an IP address, not a domain.")
	}
	return nil
}
