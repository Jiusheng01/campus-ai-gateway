package model

import "strings"

// NormalizeOptionalID collapses the three spellings of "no value" -- a nil
// pointer, the empty string, and a whitespace-only string -- into a single nil.
//
// Both the storage layer and the admin handlers need this: a JSON body that
// sends "" for an optional id means the same thing as omitting the field, and a
// NULL column is how that is stored. Keeping one implementation means the two
// layers cannot drift apart on a question as dull and as load-bearing as "does a
// blank id mean inherit or does it mean the empty id".
func NormalizeOptionalID(v *string) *string {
	if v == nil {
		return nil
	}
	if s := strings.TrimSpace(*v); s != "" {
		return &s
	}
	return nil
}

// NormalizeModelList trims and de-duplicates a model allowlist.
//
// A nil list stays nil, because that is "inherit" and must not be confused with
// the empty list, which is "deny every model". An empty-but-non-nil input
// therefore comes back as an empty non-nil slice: the caller asked to deny
// everything, and normalising that to nil would silently turn it into the
// opposite.
func NormalizeModelList(models []string) []string {
	if models == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}
