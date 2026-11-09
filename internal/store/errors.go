package store

import (
	"errors"
	"fmt"
	"strings"
)

// The sentinels every handler maps onto a status code, in one place.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
	ErrInvalid  = errors.New("invalid")
)

// These wrap a sentinel with a written sentence, which is what reaches whoever reads it: "That
// address is taken." rather than "conflict: users".
func NotFound(format string, a ...any) error { return classify(ErrNotFound, format, a...) }
func Conflict(format string, a ...any) error { return classify(ErrConflict, format, a...) }
func Invalid(format string, a ...any) error  { return classify(ErrInvalid, format, a...) }

func classify(kind error, format string, a ...any) error {
	return &classified{kind: kind, msg: fmt.Sprintf(format, a...)}
}

type classified struct {
	kind error
	msg  string
}

func (c *classified) Error() string { return c.msg }
func (c *classified) Unwrap() error { return c.kind }

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
