// Package seal encrypts what mailio has to be able to read back: the relay's password, which it
// hands to the relay. See docs/deploy.md.
package seal

import (
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// KeySize is the server key's length in bytes.
const KeySize = chacha20poly1305.KeySize

// version leads every sealed value, so a second scheme can be told apart from this one.
const version byte = 1

// ErrOpen is every failure to open: a different key, a different row, or a damaged value. One
// error, because none of them can be acted on differently from here.
var ErrOpen = errors.New("seal: cannot open")

// Sealer seals and opens with one key.
type Sealer struct {
	aead cipher.AEAD
}

func New(key []byte) (*Sealer, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("a server key is %d bytes, and this one is %d", KeySize, len(key))
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext bound to aad.
//
// The binding is what keeps a sealed value where it was put: copied into another row, which
// passes another aad, it does not open. XChaCha's nonce is long enough to be random.
func (s *Sealer) Seal(plaintext, aad []byte) []byte {
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	rand.Read(nonce)
	out := append([]byte{version}, nonce...)
	return s.aead.Seal(out, nonce, plaintext, bound(aad))
}

// Open decrypts what Seal produced with the same key and aad.
func (s *Sealer) Open(sealed, aad []byte) ([]byte, error) {
	head := 1 + chacha20poly1305.NonceSizeX
	if len(sealed) < head+s.aead.Overhead() || sealed[0] != version {
		return nil, ErrOpen
	}
	plain, err := s.aead.Open(nil, sealed[1:head], sealed[head:], bound(aad))
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}

// bound puts the version under the tag with the caller's aad, so it cannot be swapped either.
func bound(aad []byte) []byte { return append([]byte{version}, aad...) }
