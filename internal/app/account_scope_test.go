package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// The tenant_admin role and its scope are one grant. These tests pin the
// pairing down at the two places it can be broken: the validator, which decides
// whether a request is acceptable, and the service, which decides what the row
// looks like afterwards.

func TestValidateAccountScope(t *testing.T) {
	const scope = "11111111-1111-1111-1111-111111111111"
	cases := []struct {
		name  string
		roles []string
		scope string
		want  bool
	}{
		{"tenant_admin with a scope", []string{RoleTenantAdmin}, scope, true},
		{"tenant_admin without a scope", []string{RoleTenantAdmin}, "", false},
		{"tenant_admin with a blank scope", []string{RoleTenantAdmin}, "   ", false},
		{"tenant_admin beside another admin role", []string{RoleTenantAdmin, RoleGatewayAdmin}, scope, true},
		// A scope on a role that is not bounded by a tenant is a boundary that
		// nothing enforces, and it would sit in the database looking real.
		{"gateway_admin with a scope", []string{RoleGatewayAdmin}, scope, false},
		{"super_admin with a scope", []string{RoleSuperAdmin}, scope, false},
		{"member with a scope", []string{RoleMember}, scope, false},
		{"member without a scope", []string{RoleMember}, "", true},
		{"auditor without a scope", []string{RoleAuditor}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAccountScope(tc.roles, tc.scope)
			if got := err == nil; got != tc.want {
				t.Fatalf("ValidateAccountScope(%v, %q) error = %v, want acceptable = %v", tc.roles, tc.scope, err, tc.want)
			}
		})
	}
}

// tenantAdminFixture creates an account holding gateway_admin and returns the
// service that will change it.
func tenantAdminFixture(t *testing.T) (*adminAuthService, *fakeAdminStore, string) {
	t.Helper()
	s, store, _ := newTestServerWithRegistration(t, testConfig())
	seedSetupAdmin(t, s, "campus-admin", "Campus#2026Gate")

	hash, err := security.HashPassword("Campus#2026Gate")
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.CreateUser(context.Background(), "zhangsan", "张三", hash, []string{RoleGatewayAdmin}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s.adminAuth, store, user.ID
}

func TestPromotingToTenantAdminWritesTheScope(t *testing.T) {
	auth, store, id := tenantAdminFixture(t)
	scope := "11111111-1111-1111-1111-111111111111"

	roles := []string{RoleTenantAdmin}
	updated, changes, err := auth.updateUser(context.Background(), id, userUpdate{
		Roles:         &roles,
		ScopeTenantID: &scope,
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if !changes.RolesChanged || !changes.ScopeChanged {
		t.Fatalf("changes = %+v, want both roles and scope reported", changes)
	}
	if updated.ScopeTenantID == nil || *updated.ScopeTenantID != scope {
		t.Fatalf("scope = %v, want %s", updated.ScopeTenantID, scope)
	}
	if !hasRole(updated.Roles, RoleTenantAdmin) {
		t.Fatalf("roles = %v, want tenant_admin", updated.Roles)
	}
	if !changes.AuthenticationChanged() {
		t.Fatal("a role change must invalidate credentials")
	}

	// The scope must survive a read: this is the value the next request's
	// principal is built from.
	stored, err := store.FindAdminUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ScopeTenantID == nil || *stored.ScopeTenantID != scope {
		t.Fatalf("stored scope = %v, want %s", stored.ScopeTenantID, scope)
	}
}

// A patch that only moves the scope must not be read as "remove the role", and
// must not need the client to resend a role list it is not changing.
func TestChangingOnlyTheScopeKeepsTheRole(t *testing.T) {
	auth, store, id := tenantAdminFixture(t)
	first := "11111111-1111-1111-1111-111111111111"
	second := "22222222-2222-2222-2222-222222222222"

	roles := []string{RoleTenantAdmin}
	if _, _, err := auth.updateUser(context.Background(), id, userUpdate{Roles: &roles, ScopeTenantID: &first}); err != nil {
		t.Fatalf("seed scope: %v", err)
	}

	updated, changes, err := auth.updateUser(context.Background(), id, userUpdate{ScopeTenantID: &second})
	if err != nil {
		t.Fatalf("move scope: %v", err)
	}
	if changes.RolesChanged {
		t.Fatal("moving the scope must not report a role change")
	}
	if !changes.ScopeChanged {
		t.Fatal("moving the scope must report a scope change")
	}
	if updated.ScopeTenantID == nil || *updated.ScopeTenantID != second {
		t.Fatalf("scope = %v, want %s", updated.ScopeTenantID, second)
	}
	if !hasRole(updated.Roles, RoleTenantAdmin) {
		t.Fatalf("roles = %v, want the role kept", updated.Roles)
	}
	if changes.AuthenticationChanged() {
		t.Fatal("a scope-only change must not sign the administrator out: the principal is re-read every request")
	}

	stored, err := store.FindAdminUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ScopeTenantID == nil || *stored.ScopeTenantID != second {
		t.Fatalf("stored scope = %v, want %s", stored.ScopeTenantID, second)
	}
}

// Demoting a tenant_admin must clear the scope even though the request never
// mentions it. A leftover scope on an account that is no longer a tenant_admin
// is a boundary that looks real and is not.
func TestDemotingClearsTheScope(t *testing.T) {
	auth, store, id := tenantAdminFixture(t)
	scope := "11111111-1111-1111-1111-111111111111"

	roles := []string{RoleTenantAdmin}
	if _, _, err := auth.updateUser(context.Background(), id, userUpdate{Roles: &roles, ScopeTenantID: &scope}); err != nil {
		t.Fatalf("seed scope: %v", err)
	}

	member := []string{RoleMember}
	updated, changes, err := auth.updateUser(context.Background(), id, userUpdate{Roles: &member})
	if err != nil {
		t.Fatalf("demote: %v", err)
	}
	if !changes.ScopeChanged {
		t.Fatal("demoting must report the scope being cleared")
	}
	if updated.ScopeTenantID != nil {
		t.Fatalf("scope = %v, want nil", *updated.ScopeTenantID)
	}

	stored, err := store.FindAdminUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ScopeTenantID != nil {
		t.Fatalf("stored scope = %v, want nil", *stored.ScopeTenantID)
	}
}

func TestTenantAdminWithoutAScopeIsRefused(t *testing.T) {
	auth, _, id := tenantAdminFixture(t)

	roles := []string{RoleTenantAdmin}
	_, _, err := auth.updateUser(context.Background(), id, userUpdate{Roles: &roles})
	if !errors.Is(err, ErrAdminBadRequest) {
		t.Fatalf("err = %v, want ErrAdminBadRequest", err)
	}
}

func TestScopeOnANonTenantAdminIsRefused(t *testing.T) {
	auth, _, id := tenantAdminFixture(t)
	scope := "11111111-1111-1111-1111-111111111111"

	_, _, err := auth.updateUser(context.Background(), id, userUpdate{ScopeTenantID: &scope})
	if !errors.Is(err, ErrAdminBadRequest) {
		t.Fatalf("err = %v, want ErrAdminBadRequest", err)
	}
}

// A scope naming a tenant that does not exist is a 400 about the field the
// operator got wrong, not a 500 about a foreign key.
func TestScopeMustNameAnExistingTenant(t *testing.T) {
	auth, store, id := tenantAdminFixture(t)
	missing := "99999999-9999-9999-9999-999999999999"
	store.missingTenantID = missing

	roles := []string{RoleTenantAdmin}
	_, _, err := auth.updateUser(context.Background(), id, userUpdate{
		Roles:         &roles,
		ScopeTenantID: &missing,
	})
	if !errors.Is(err, ErrAdminTenantNotFound) {
		t.Fatalf("err = %v, want ErrAdminTenantNotFound", err)
	}
}
