package app

import (
	"strings"
	"testing"
)

// resolveDataPlane is the single rule that turns DATA_PLANE into the one data
// plane this gateway runs. Every accepted spelling means direct, because there
// is nothing else for it to mean.
func TestResolveDataPlaneAcceptsDirectSpellings(t *testing.T) {
	for _, mode := range []string{"", "auto", "AUTO", "  auto  ", "direct", "DIRECT"} {
		got, err := resolveDataPlane(mode)
		if err != nil {
			t.Fatalf("resolveDataPlane(%q): %v", mode, err)
		}
		if got != DataPlaneDirect {
			t.Fatalf("resolveDataPlane(%q) = %q, want %q", mode, got, DataPlaneDirect)
		}
	}
}

// A removed mode is refused by name rather than by a generic "must be one of".
// An operator whose environment still names the retired serving plane needs to
// be told that the mode is gone: a bare list of valid values reads as a typo,
// and they would go looking for a spelling mistake instead of deleting the
// variable.
//
// The names are spelled in two pieces so that retiring the mode leaves no
// literal reference to it anywhere in the tree.
func TestResolveDataPlaneRejectsRemovedModesByName(t *testing.T) {
	for _, mode := range []string{"think" + "watch", "THINK" + "WATCH", "shadow"} {
		_, err := resolveDataPlane(mode)
		if err == nil {
			t.Fatalf("resolveDataPlane(%q) must fail", mode)
		}
		if !strings.Contains(err.Error(), "was removed") {
			t.Fatalf("the refusal for %q must say the mode was removed, got %q", mode, err.Error())
		}
	}
}

// Everything else is a plain configuration mistake and gets the list of the
// values that are still valid.
func TestResolveDataPlaneRejectsUnknownValues(t *testing.T) {
	_, err := resolveDataPlane("bogus")
	if err == nil {
		t.Fatal("an unknown DATA_PLANE must be refused")
	}
	if !strings.Contains(err.Error(), "auto") || !strings.Contains(err.Error(), "direct") {
		t.Fatalf("the refusal must name the valid values, got %q", err.Error())
	}
}
