package api

import "encoding/json"

// optional is a PATCH field that can be left out, which leaves it alone, or sent as null, which
// returns it to the one above, or sent with a value. A pointer cannot tell the first two apart.
type optional[T any] struct {
	Set   bool
	Value *T
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}
