// Package ids mints and validates every identifier in mailio.
//
// A software-only identifier is a ULID: a prefix naming what it is, then 26 Crockford
// characters over 16 bytes. See docs/conventions.md.
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"strings"
)

// Alphabet is Crockford's base32, lowercased.
//
// It omits i, l, o and u, so an id cannot be misread between similar glyphs or accidentally
// spell a word. Lowercase because an id is read inside a URL.
const Alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// Prefixes.
const (
	Domain = "dm_"
	User   = "u_"
	Admin  = "ad_"
)

const ulidLen = 26

// New mints a prefixed ULID: a 6-byte big-endian millisecond timestamp then 10 random bytes,
// so ORDER BY id is a time order.
func New(prefix string, unixMilli int64) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(unixMilli)<<16)
	rand.Read(b[6:])
	return prefix + encode32(b[:], ulidLen)
}

// Valid reports whether id is a well-formed identifier of the given prefix.
//
// Called before a value reaches a query, so a typo is a refusal naming the problem rather than
// an empty result that looks like a missing row.
func Valid(prefix, id string) bool {
	body, ok := strings.CutPrefix(id, prefix)
	if !ok || len(body) != ulidLen {
		return false
	}
	for i := 0; i < len(body); i++ {
		if !strings.ContainsRune(Alphabet, rune(body[i])) {
			return false
		}
	}
	return true
}

// encode32 renders b as n characters of the alphabet, most significant first.
func encode32(b []byte, n int) string {
	out := make([]byte, n)
	var acc, bits uint32
	i := n
	for j := len(b) - 1; j >= 0; j-- {
		acc |= uint32(b[j]) << bits
		bits += 8
		for bits >= 5 && i > 0 {
			i--
			out[i] = Alphabet[acc&31]
			acc >>= 5
			bits -= 5
		}
	}
	for i > 0 {
		i--
		out[i] = Alphabet[acc&31]
		acc >>= 5
	}
	return string(out)
}
