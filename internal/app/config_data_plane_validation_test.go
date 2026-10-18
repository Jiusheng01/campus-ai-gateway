package app

import "testing"

// resolveDataPlane documents validateUpstreamURL as the thing that turns a
// malformed THINKWATCH_URL into a startup failure rather than a confusing dial
// error on the first business request. The auto branch, which is the default
// for every deployment that sets THINKWATCH_URL, skipped that validation. The
// same malformed URL that is refused when DATA_PLANE=thinkwatch was accepted
// when DATA_PLANE was unset or auto.
func TestDataPlaneAutoRejectsAThinkWatchURLWithoutAScheme(t *testing.T) {
	for _, mode := range []string{"", "auto"} {
		t.Run("DATA_PLANE="+mode, func(t *testing.T) {
			if _, err := loadConfigWithDataPlane(t, mode, "127.0.0.1:8788"); err == nil {
				t.Fatal("the auto branch must validate THINKWATCH_URL exactly as the explicit thinkwatch branch does")
			}
		})
	}
}

// A malformed URL with a scheme but no host is the other shape url.Parse
// accepts and the proxy then cannot dial.
func TestDataPlaneAutoRejectsAThinkWatchURLWithoutAHost(t *testing.T) {
	if _, err := loadConfigWithDataPlane(t, "auto", "http:///missing-host"); err == nil {
		t.Fatal("a THINKWATCH_URL with no host must be rejected at startup")
	}
}

// The valid forms must keep working, so the fix is validation rather than a
// blanket refusal.
func TestDataPlaneAutoAcceptsAWellFormedThinkWatchURL(t *testing.T) {
	cfg, err := loadConfigWithDataPlane(t, "auto", "http://127.0.0.1:8788")
	if err != nil {
		t.Fatalf("a well-formed URL must still be accepted: %v", err)
	}
	if !cfg.UsesThinkWatch() {
		t.Fatal("auto with a URL must resolve to the ThinkWatch plane")
	}
}
