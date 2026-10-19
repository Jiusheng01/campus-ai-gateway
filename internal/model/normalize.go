package model

import "strings"

// NormalizeOptionalID collapses the three spellings of "no value" -- a nil
// pointer, the empty string, and a whitespace-only string -- into a single nil.
//
// Both the storage layer and the admin handlers need this: a JSON body that
// sends "" for an optional id means the same thing as omitting the field, and a
// NULL column is how that is stored. Keeping one implementation means the two
// layers cannot drift apart on a question as dull and as load-bearing as "does
// a blank id mean inherit or does it mean the empty id".
func NormalizeOptionalID(v *string) *string {
	if v == nil {
		return nil
	}
	if s := strings.TrimSpace(*v); s != "" {
		return &s
	}
	return nil
}
