package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// --- auth_version semantics -------------------------------------------------

// One PATCH is one logical change. auth_version invalidates credentials, so it
// has to move when — and only when — something that credentials depend on has
// actually changed. Bumping it twice for a single request would be harmless to
// the user, but bumping it for a display-name edit signs them out of a device
// they are sitting in front of, which is exactly the bug this pins down.
func TestAuthVersionSemantics(t *testing.T) {
	cases := []struct {
		name        string
		roles       *[]string
		status      *int
		displayName *string
		wantVersion int64
		wantRevoked bool
	}{
		{
			name:        "display name only",
			displayName: ptr("张三老师"),
			wantVersion: 0,
			wantRevoked: false,
		},
		{
			name:        "roles only",
			roles:       ptrSlice([]string{RoleGatewayAdmin}),
			wantVersion: 1,
			wantRevoked: true,
		},
		{
			name:        "status only",
			status:      ptr(model.AdminStatusDisabled),
			wantVersion: 1,
			wantRevoked: true,
		},
		{
			name:        "roles and status",
			roles:       ptrSlice([]string{RoleGatewayAdmin}),
			status:      ptr(model.AdminStatusDisabled),
			wantVersion: 1,
			wantRevoked: true,
		},
		{
			name:        "roles and display name",
			roles:       ptrSlice([]string{RoleGatewayAdmin}),
			displayName: ptr("张三老师"),
			wantVersion: 1,
			wantRevoked: true,
		},
		{
			name:        "status and display name",
			status:      ptr(model.AdminStatusDisabled),
			displayName: ptr("停用测试用户"),
			wantVersion: 1,
			wantRevoked: true,
		},
		{
			name:        "all three",
			roles:       ptrSlice([]string{RoleGatewayAdmin}),
			status:      ptr(model.AdminStatusDisabled),
			displayName: ptr("张三老师"),
			wantVersion: 1,
			wantRevoked: true,
		},
		{
			name:        "no-op",
			roles:       ptrSlice([]string{RoleMember}),
			status:      ptr(model.AdminStatusEnabled),
			displayName: ptr("Test Member"),
			wantVersion: 0,
			wantRevoked: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, store, _ := newTestServerWithRegistration(t, testConfig())
			seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
			member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

			// Two live sessions, so a revocation is observable.
			accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
			accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
			before := member.AuthVersion

			updated, changes, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{
				Roles:       tc.roles,
				Status:      tc.status,
				DisplayName: tc.displayName,
			})
			if err != nil {
				t.Fatalf("update: %v", err)
			}

			if got := updated.AuthVersion - before; got != tc.wantVersion {
				t.Fatalf("auth_version moved by %d, want %d (change set %+v)", got, tc.wantVersion, changes)
			}

			live := store.liveSessions()
			if tc.wantRevoked && live != 0 {
				t.Fatalf("%d sessions still live, want all revoked", live)
			}
			if !tc.wantRevoked && live != 2 {
				t.Fatalf("%d sessions live, want both kept", live)
			}
		})
	}
}

// --- session semantics ------------------------------------------------------

func TestDisplayNameOnlyKeepsSessionsAndTokensUsable(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
	before, err := store.FindAdminUserByID(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}

	name := "张三老师"
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{DisplayName: &name}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// The account is still signed in: the token it already holds keeps working
	// and the session is still listed.
	if rec := callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/whoami", ""); rec.Code != http.StatusOK {
		t.Fatalf("the existing token stopped working after a rename: %d %s", rec.Code, rec.Body.String())
	}
	if live := store.liveSessions(); live != 1 {
		t.Fatalf("%d live sessions, want 1", live)
	}
	after, err := store.FindAdminUserByID(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AuthVersion != before.AuthVersion {
		t.Fatalf("auth_version moved on a rename: %d -> %d", before.AuthVersion, after.AuthVersion)
	}
	if after.DisplayName != name {
		t.Fatalf("display name = %q, want %q", after.DisplayName, name)
	}
}

func TestNoopRoleAndStatusUpdatesKeepSessions(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	hash, err := security.HashPassword("Campus#2026Gate")
	if err != nil {
		t.Fatal(err)
	}
	operator, err := store.CreateUser(context.Background(), "zhangsan", "张三", hash, []string{RoleGatewayAdmin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	// Exactly the roles the account already holds.
	sameRoles := []string{RoleGatewayAdmin}
	if _, changes, err := s.adminAuth.updateUser(context.Background(), operator.ID, userUpdate{Roles: &sameRoles}); err != nil {
		t.Fatalf("no-op roles: %v", err)
	} else if changes.RolesChanged {
		t.Fatal("resending the same roles must not report a change")
	}
	// Exactly the status the account already has.
	sameStatus := model.AdminStatusEnabled
	if _, changes, err := s.adminAuth.updateUser(context.Background(), operator.ID, userUpdate{Status: &sameStatus}); err != nil {
		t.Fatalf("no-op status: %v", err)
	} else if changes.StatusChanged {
		t.Fatal("resending the same status must not report a change")
	}

	if rec := callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/providers", ""); rec.Code != http.StatusOK {
		t.Fatalf("a no-op must not invalidate the token: %d %s", rec.Code, rec.Body.String())
	}
	if live := store.liveSessions(); live != 1 {
		t.Fatalf("%d live sessions, want 1", live)
	}
	after, err := store.FindAdminUserByID(context.Background(), operator.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.AuthVersion != operator.AuthVersion {
		t.Fatalf("auth_version moved on a no-op: %d -> %d", operator.AuthVersion, after.AuthVersion)
	}
}

func TestRoleChangeInvalidatesExistingTokensImmediately(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	hash, err := security.HashPassword("Campus#2026Gate")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateUser(context.Background(), "zhangsan", "张三", hash, []string{RoleGatewayAdmin}, nil)
	if err != nil {
		t.Fatal(err)
	}

	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
	if rec := callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/providers", ""); rec.Code != http.StatusOK {
		t.Fatalf("precondition: %d", rec.Code)
	}

	demoted := []string{RoleMember}
	if _, _, err := s.adminAuth.updateUser(context.Background(), target.ID, userUpdate{Roles: &demoted}); err != nil {
		t.Fatalf("demote: %v", err)
	}

	// Authority taken away must stop working at once, not at token expiry.
	rec := callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/providers", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("the demoted account's old token still works: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPromotedAccountMustSignInAgainForTheNewAuthority(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	oldToken := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	promoted := []string{RoleGatewayAdmin}
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Roles: &promoted}); err != nil {
		t.Fatalf("promote: %v", err)
	}

	// The session that existed before the promotion carries the old authority
	// and must not silently acquire the new one.
	if rec := callThroughAdminMiddleware(t, s, oldToken, http.MethodGet, "/admin/providers", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the pre-promotion token is still accepted: %d", rec.Code)
	}

	fresh := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")
	if rec := callThroughAdminMiddleware(t, s, fresh, http.MethodGet, "/admin/providers", ""); rec.Code != http.StatusOK {
		t.Fatalf("a fresh sign-in must carry the new role: %d %s", rec.Code, rec.Body.String())
	}
}

func TestDisabledAccountLosesTokensAndCannotSignIn(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	disabled := model.AdminStatusDisabled
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Status: &disabled}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if rec := callThroughAdminMiddleware(t, s, token, http.MethodGet, "/admin/whoami", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a disabled account's token still works: %d", rec.Code)
	}
	if _, err := s.adminAuth.login(context.Background(), "zhangsan", "Campus#2026Gate", "10.0.0.9", "ua"); !errors.Is(err, ErrAdminUserDisabled) {
		t.Fatalf("expected ErrAdminUserDisabled, got %v", err)
	}

	enabled := model.AdminStatusEnabled
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Status: &enabled}); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if _, err := s.adminAuth.login(context.Background(), "zhangsan", "Campus#2026Gate", "10.0.0.9", "ua"); err != nil {
		t.Fatalf("a re-enabled account must be able to sign in: %v", err)
	}
}

// A combined patch revokes once. The observable consequence is the reason
// recorded on the session, which must name the combined change rather than
// whichever field happened to be applied first.
func TestCombinedChangeRevokesOnceWithOneReason(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")
	accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	roles := []string{RoleGatewayAdmin}
	disabled := model.AdminStatusDisabled
	if _, changes, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{
		Roles:  &roles,
		Status: &disabled,
	}); err != nil {
		t.Fatalf("combined update: %v", err)
	} else if !changes.RolesChanged || !changes.StatusChanged {
		t.Fatalf("change set = %+v", changes)
	}

	if got := store.revokeCallsFor(member.ID); got != 1 {
		t.Fatalf("RevokeSessionsForUser called %d times, want exactly 1", got)
	}
	if reason := store.lastRevokeReason(member.ID); reason != revokeReasonSecurityChanged {
		t.Fatalf("revoke reason = %q, want %q", reason, revokeReasonSecurityChanged)
	}
}

func TestRevokeReasonNamesTheActualChange(t *testing.T) {
	cases := []struct {
		name    string
		changes storage.AdminUserChangeSet
		want    string
	}{
		{"roles only", storage.AdminUserChangeSet{RolesChanged: true}, revokeReasonRoleChanged},
		{"disabled", storage.AdminUserChangeSet{StatusChanged: true, NewStatus: model.AdminStatusDisabled}, revokeReasonDisabled},
		{"enabled", storage.AdminUserChangeSet{StatusChanged: true, NewStatus: model.AdminStatusEnabled}, revokeReasonStatusChanged},
		{"both", storage.AdminUserChangeSet{RolesChanged: true, StatusChanged: true}, revokeReasonSecurityChanged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := revokeReasonForChange(tc.changes); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- audit matrix -----------------------------------------------------------

// The audit trail is derived from the change set, so it cannot claim something
// happened that did not, and it cannot lose half of a multi-field request.
func TestAccountAuditActionsFollowTheChangeSet(t *testing.T) {
	cases := []struct {
		name    string
		changes storage.AdminUserChangeSet
		want    []string
	}{
		{
			name:    "profile only",
			changes: storage.AdminUserChangeSet{DisplayNameChanged: true},
			want:    []string{"user.profile.update"},
		},
		{
			name:    "roles only",
			changes: storage.AdminUserChangeSet{RolesChanged: true},
			want:    []string{"user.role.update"},
		},
		{
			name:    "disable",
			changes: storage.AdminUserChangeSet{StatusChanged: true, NewStatus: model.AdminStatusDisabled},
			want:    []string{"user.disable"},
		},
		{
			name:    "enable",
			changes: storage.AdminUserChangeSet{StatusChanged: true, NewStatus: model.AdminStatusEnabled},
			want:    []string{"user.enable"},
		},
		{
			name: "roles and disable",
			changes: storage.AdminUserChangeSet{
				RolesChanged: true, StatusChanged: true, NewStatus: model.AdminStatusDisabled,
			},
			want: []string{"user.role.update", "user.disable"},
		},
		{
			name: "roles and profile",
			changes: storage.AdminUserChangeSet{
				RolesChanged: true, DisplayNameChanged: true,
			},
			want: []string{"user.role.update", "user.profile.update"},
		},
		{
			name: "all three",
			changes: storage.AdminUserChangeSet{
				RolesChanged: true, StatusChanged: true, NewStatus: model.AdminStatusEnabled, DisplayNameChanged: true,
			},
			want: []string{"user.role.update", "user.enable", "user.profile.update"},
		},
		{
			name:    "no-op",
			changes: storage.AdminUserChangeSet{},
			want:    nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := accountAuditActions(tc.changes)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// --- input validation -------------------------------------------------------

func TestUpdateRejectsUnknownRolesRatherThanDroppingThem(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	// Silently turning this into ["gateway_admin"] would tell the operator their
	// request was understood when half of it was discarded.
	roles := []string{RoleGatewayAdmin, "god_mode"}
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Roles: &roles}); !errors.Is(err, ErrAdminBadRequest) {
		t.Fatalf("expected ErrAdminBadRequest, got %v", err)
	}
	after, err := store.FindAdminUserByID(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Roles) != 1 || after.Roles[0] != RoleMember {
		t.Fatalf("roles = %v, want the original [member]", after.Roles)
	}
}

func TestUpdateRejectsInvalidStatus(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	for _, status := range []int{0, 3, 999, -1} {
		bad := status
		if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{Status: &bad}); !errors.Is(err, ErrAdminBadRequest) {
			t.Fatalf("status %d: expected ErrAdminBadRequest, got %v", status, err)
		}
	}
}

func TestUpdateRejectsOverlongDisplayName(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")
	member := seedMember(t, store, "zhangsan", "Campus#2026Gate")

	// 129 runes, one past the column limit. The client is not the only validator.
	tooLong := strings.Repeat("名", 129)
	if _, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{DisplayName: &tooLong}); !errors.Is(err, ErrAdminBadRequest) {
		t.Fatalf("expected ErrAdminBadRequest, got %v", err)
	}

	// Exactly at the limit is fine, and surrounding whitespace is trimmed rather
	// than counted.
	atLimit := strings.Repeat("名", 128)
	updated, _, err := s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{DisplayName: &atLimit})
	if err != nil {
		t.Fatalf("128 runes must be accepted: %v", err)
	}
	if updated.DisplayName != atLimit {
		t.Fatalf("display name was not stored as given")
	}
	padded := "  " + atLimit + "  "
	updated, _, err = s.adminAuth.updateUser(context.Background(), member.ID, userUpdate{DisplayName: &padded})
	if err != nil {
		t.Fatalf("padded value must be trimmed then accepted: %v", err)
	}
	if updated.DisplayName != atLimit {
		t.Fatalf("display name = %q, want the trimmed value", updated.DisplayName)
	}
}

// A gateway administrator must not be able to promote itself. The role check
// happens in the middleware, so the request never reaches the mutation at all.
func TestGatewayAdminCannotPromoteItself(t *testing.T) {
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	hash, err := security.HashPassword("Campus#2026Gate")
	if err != nil {
		t.Fatal(err)
	}
	gatewayAdmin, err := store.CreateUser(context.Background(), "zhangsan", "张三", hash, []string{RoleGatewayAdmin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	token := accessTokenFor(t, s, "zhangsan", "Campus#2026Gate")

	body := `{"roles":["super_admin"]}`
	rec := callThroughAdminMiddleware(t, s, token, http.MethodPatch, "/admin/users/"+gatewayAdmin.ID, body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("self-promotion returned %d, want 403: %s", rec.Code, rec.Body.String())
	}

	after, err := store.FindAdminUserByID(context.Background(), gatewayAdmin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hasSuperAdmin(after.Roles) {
		t.Fatalf("roles = %v: the request must not have reached the mutation", after.Roles)
	}
}

// --- model test options -----------------------------------------------------

// The option view is a projection, not a re-serialisation of the model config.
// This is the check that a field added to ModelConfig later cannot leak through
// the restricted endpoint by default.
func TestModelTestOptionViewDropsEverythingElse(t *testing.T) {
	full := model.ModelConfig{
		ID:          "36d2e51c-b6b7-4409-a871-a088616cd9cd",
		Alias:       "campus-smart",
		DisplayName: "校园智能助手",
		Status:      1,
	}
	view := modelTestOptionView{ID: full.ID, Alias: full.Alias, Status: full.Status}

	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	for _, forbidden := range []string{
		"provider_id", "provider_name", "upstream_model", "display_name",
		"credential_env", "base_url", "api_key", "secret", "password",
		"route_group",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the option view exposes %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{"id", "alias", "status"} {
		if !strings.Contains(body, `"`+required+`"`) {
			t.Fatalf("the option view is missing %q: %s", required, body)
		}
	}

	var keys map[string]any
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Fatalf("the option view has %d fields, want exactly 3: %v", len(keys), keys)
	}
}

func TestModelTestOptionsPermissions(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{RoleSuperAdmin, true},
		{RoleGatewayAdmin, true},
		{RoleOperator, true},
		{RoleTenantAdmin, false},
		{RoleAuditor, false},
		{RoleMember, false},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/model-test-options", nil)
			p := adminPrincipal{UserID: "u", User: tc.role, Roles: map[string]bool{tc.role: true}}
			if got := p.can(req); got != tc.want {
				t.Fatalf("%s may read model-test-options = %v, want %v", tc.role, got, tc.want)
			}
		})
	}
}

// The operator's whole point is running the test. Reading the model
// configuration is a different capability and stays out of reach.
func TestOperatorCanTestModelsButNotAdministerThem(t *testing.T) {
	operator := adminPrincipal{UserID: "u", User: "ops", Roles: map[string]bool{RoleOperator: true}}

	allowed := []struct{ method, path string }{
		{http.MethodGet, "/admin/status"},
		{http.MethodGet, "/admin/model-test-options"},
		{http.MethodPost, "/admin/model-test"},
	}
	for _, tc := range allowed {
		if !operator.can(httptest.NewRequest(tc.method, tc.path, nil)) {
			t.Fatalf("operator must be allowed %s %s", tc.method, tc.path)
		}
	}

	denied := []struct{ method, path string }{
		{http.MethodGet, "/admin/models"},
		{http.MethodPost, "/admin/models"},
		{http.MethodGet, "/admin/providers"},
		{http.MethodGet, "/admin/model-routes"},
		{http.MethodPost, "/admin/providers"},
		{http.MethodGet, "/admin/tenants"},
		{http.MethodGet, "/admin/api-keys"},
		{http.MethodGet, "/admin/request-logs"},
		{http.MethodGet, "/admin/admin-audit"},
		{http.MethodGet, "/admin/security-events"},
		{http.MethodGet, "/admin/users"},
		{http.MethodPatch, "/admin/users/x"},
	}
	for _, tc := range denied {
		if operator.can(httptest.NewRequest(tc.method, tc.path, nil)) {
			t.Fatalf("operator must be denied %s %s", tc.method, tc.path)
		}
	}
}

// --- helpers ----------------------------------------------------------------

func ptrSlice(v []string) *[]string { return &v }

func ptr[T any](v T) *T { return &v }
