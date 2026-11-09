package seal

import (
	"bytes"
	"errors"
	"testing"
)

func sealer(t *testing.T, fill byte) *Sealer {
	t.Helper()
	s, err := New(bytes.Repeat([]byte{fill}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWhatIsSealedOpensWithTheSameKeyAndAAD(t *testing.T) {
	s := sealer(t, 1)
	sealed := s.Seal([]byte("hunter2"), []byte("ec_1/incoming"))
	got, err := s.Open(sealed, []byte("ec_1/incoming"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hunter2" {
		t.Errorf("opened %q", got)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Error("the plaintext is readable in what was sealed")
	}
}

// A password copied from one row into another must not open there.
func TestAnotherAADDoesNotOpen(t *testing.T) {
	s := sealer(t, 1)
	sealed := s.Seal([]byte("hunter2"), []byte("ec_1/incoming"))
	for _, aad := range []string{"ec_2/incoming", "ec_1/outgoing", ""} {
		if _, err := s.Open(sealed, []byte(aad)); !errors.Is(err, ErrOpen) {
			t.Errorf("opened under %q: %v", aad, err)
		}
	}
}

func TestAnotherKeyDoesNotOpen(t *testing.T) {
	sealed := sealer(t, 1).Seal([]byte("hunter2"), []byte("a"))
	if _, err := sealer(t, 2).Open(sealed, []byte("a")); !errors.Is(err, ErrOpen) {
		t.Errorf("another key opened it: %v", err)
	}
}

func TestADamagedValueDoesNotOpen(t *testing.T) {
	s := sealer(t, 1)
	sealed := s.Seal([]byte("hunter2"), []byte("a"))
	for i := range sealed {
		damaged := bytes.Clone(sealed)
		damaged[i] ^= 1
		if _, err := s.Open(damaged, []byte("a")); !errors.Is(err, ErrOpen) {
			t.Fatalf("opened with byte %d flipped", i)
		}
	}
	if _, err := s.Open(sealed[:10], []byte("a")); !errors.Is(err, ErrOpen) {
		t.Error("opened a truncated value")
	}
}

// The same password sealed twice must not look the same, or equal passwords show as equal rows.
func TestSealingTwiceGivesTwoValues(t *testing.T) {
	s := sealer(t, 1)
	if bytes.Equal(s.Seal([]byte("x"), nil), s.Seal([]byte("x"), nil)) {
		t.Error("two seals of one value are identical")
	}
}

func TestAKeyOfTheWrongLengthIsRefused(t *testing.T) {
	if _, err := New(make([]byte, 16)); err == nil {
		t.Error("accepted a 16-byte key")
	}
}
