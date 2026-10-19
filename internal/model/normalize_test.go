package model

import "testing"

func TestNormalizeOptionalID(t *testing.T) {
	if got := NormalizeOptionalID(nil); got != nil {
		t.Fatalf("nil = %v, want nil", *got)
	}
	for _, blank := range []string{"", "   ", "\t\n"} {
		blank := blank
		if got := NormalizeOptionalID(&blank); got != nil {
			t.Fatalf("%q = %v, want nil", blank, *got)
		}
	}
	// Surrounding whitespace is trimmed, but the interior is left alone: an id
	// with a space inside it is malformed, not something to silently repair.
	padded := "  tenant-1  "
	got := NormalizeOptionalID(&padded)
	if got == nil || *got != "tenant-1" {
		t.Fatalf("padded id = %v, want tenant-1", got)
	}
	// The returned pointer must not alias the caller's value.
	*got = "mutated"
	if padded != "  tenant-1  " {
		t.Fatalf("NormalizeOptionalID aliased the input: %q", padded)
	}
}
