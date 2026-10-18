package app

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// Tenant status is stored as 1 for enabled and 2 for disabled, so the zero
// value is neither of them. Before this release the data plane never read the
// column at all; the risk in adding it is the opposite reading, where a status
// that was never loaded looks like "not disabled" and the tenant keeps serving.
func TestTenantEnabledOnlyAcceptsTheEnabledStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   bool
	}{
		{"enabled", 1, true},
		{"disabled", 2, false},
		{"never loaded, i.e. the zero value", 0, false},
		{"a value nobody defined", 9, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := model.APIKey{TenantStatus: tc.status}
			if got := k.TenantEnabled(); got != tc.want {
				t.Fatalf("TenantEnabled() with tenant status %d = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// A key being enabled cannot rescue a disabled tenant, and the key being
// disabled does not change how the tenant question is answered either. The two
// are separate questions: the middleware refuses a disabled key on its own
// grounds, and a disabled tenant on theirs.
func TestTenantVerdictIgnoresTheKeysOwnStatus(t *testing.T) {
	for _, keyStatus := range []int{1, 2, 0} {
		k := model.APIKey{Status: keyStatus, TenantStatus: 2}
		if k.TenantEnabled() {
			t.Fatalf("a disabled tenant must be refused whatever the key's own status is (key status %d)", keyStatus)
		}
	}
}
