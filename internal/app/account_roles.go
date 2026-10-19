package app

import (
	"sort"
	"strings"
)

// Account roles.
//
// One account model serves both ordinary users and administrators: an account
// is an administrator because of the roles it holds, not because it lives in a
// different table. "member" is the role of an account with no management
// privilege, and it is mutually exclusive with every management role — a list
// like ["member","gateway_admin"] would leave two different answers to "is this
// account an administrator" depending on which check ran first.
//
// The names are identical to the ones ADMIN_AUTH_MODE=trusted-header receives
// from the campus proxy, so an SSO rollout later does not have to re-map them.
const (
	RoleMember       = "member"
	RoleAuditor      = "auditor"
	RoleOperator     = "operator"
	RoleTenantAdmin  = "tenant_admin"
	RoleGatewayAdmin = "gateway_admin"
	RoleSuperAdmin   = "super_admin"
)

// managementRoles are the roles that grant control-plane access. Holding any of
// them makes the account an administrator, which is what removes the need for
// "member".
//
// tenant_admin is a management role, but not a global one. Its authority is
// confined to the subtree named by the account's scope_tenant_id: it may read
// and write the tenants, API keys and model quotas inside that subtree and is
// refused everywhere else. Because the role without a scope would mean "every
// tenant", the two are inseparable — ValidateAccountScope refuses a role list
// that names tenant_admin without a scope, and refuses a scope on an account
// that is not a tenant_admin.
var managementRoles = []string{
	RoleAuditor,
	RoleOperator,
	RoleTenantAdmin,
	RoleGatewayAdmin,
	RoleSuperAdmin,
}

// managementRoleSet is managementRoles as a lookup, so "does this role grant
// control-plane access" has exactly one definition.
var managementRoleSet = func() map[string]bool {
	m := make(map[string]bool, len(managementRoles))
	for _, r := range managementRoles {
		m[r] = true
	}
	return m
}()

// unassignableRoles are recognised but may not be handed out through the user
// management API. A super administrator must not be able to grant a role that
// reads like authority while granting none: the account would look privileged
// in the console and behave as a member everywhere else.
//
// It is empty now that tenant_admin carries a real, scoped authority, but the
// mechanism stays: withdrawing a role from the assignable vocabulary is a
// decision that will be made again, and it should not require re-inventing the
// check.
var unassignableRoles = map[string]bool{}

// rolePrecedence fixes the order roles are listed in, most privileged first.
// Sorting by it keeps API responses and the UI stable instead of leaking Go's
// map iteration order. It covers every recognised name, including the ones that
// no longer grant anything, because it is about display order rather than
// authority.
var rolePrecedence = map[string]int{
	RoleSuperAdmin:   0,
	RoleGatewayAdmin: 1,
	RoleTenantAdmin:  2,
	RoleOperator:     3,
	RoleAuditor:      4,
	RoleMember:       5,
}

// IsManagementRole reports whether a role grants control-plane access.
func IsManagementRole(role string) bool {
	return managementRoleSet[strings.TrimSpace(role)]
}

// isKnownRole reports whether the role is part of the vocabulary. Anything else
// is dropped rather than stored, so a typo cannot sit in the database looking
// like a permission.
func isKnownRole(role string) bool {
	_, ok := rolePrecedence[strings.TrimSpace(role)]
	return ok
}

// NormalizeAccountRoles turns an arbitrary role list into the canonical one.
//
// This runs on every path that writes roles — public registration, the user
// management API and the trusted-header parser — because the client is never
// trusted to decide what a role list means. The rules:
//
//   - unknown roles are dropped;
//   - duplicates collapse;
//   - "member" is removed as soon as any management role is present;
//   - an empty result becomes ["member"], because an account always has a role.
func NormalizeAccountRoles(roles []string) []string {
	set := make(map[string]bool, len(roles))
	for _, raw := range roles {
		role := strings.TrimSpace(raw)
		if isKnownRole(role) {
			set[role] = true
		}
	}

	for _, role := range managementRoles {
		if set[role] {
			delete(set, RoleMember)
			break
		}
	}
	if len(set) == 0 {
		set[RoleMember] = true
	}

	out := make([]string, 0, len(set))
	for role := range set {
		out = append(out, role)
	}
	sort.Slice(out, func(i, j int) bool {
		if rolePrecedence[out[i]] != rolePrecedence[out[j]] {
			return rolePrecedence[out[i]] < rolePrecedence[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// ValidateAccountRoles rejects a role list that cannot be granted through the
// user management API.
//
// NormalizeAccountRoles drops what it does not recognise, which is the right
// behaviour when reading a stored list or a value from a trusted proxy — there
// is nothing useful to do with an unknown string at that point. It is the wrong
// behaviour for an explicit administrator request: silently turning
// ["gateway_admin","god_mode"] into ["gateway_admin"] tells the operator their
// input was accepted when part of it was discarded, and it hides a typo or a
// client bug behind an apparent success. The API refuses it instead.
//
// The same reasoning applies to a recognised role that is not currently
// assignable. Returning 400 rather than quietly writing it keeps the operator
// informed and keeps the console from showing a grant that does nothing.
func ValidateAccountRoles(roles []string) error {
	for _, raw := range roles {
		role := strings.TrimSpace(raw)
		if role == "" || !isKnownRole(role) {
			return ErrAdminBadRequest
		}
		if unassignableRoles[role] {
			return ErrAdminBadRequest
		}
	}
	return nil
}

// ValidateAccountScope checks the pairing between the tenant_admin role and the
// subtree it administers.
//
// The pairing is the whole security property of the role. A tenant_admin with
// no scope would be the global grant that was withdrawn for being unbounded;
// a scope on any other role would be a restriction that nothing enforces, and
// would sit in the database looking like a boundary. Both are refused rather
// than silently repaired, for the same reason unknown roles are refused: an
// operator who asked for something the system will not do should be told.
//
// scopeTenantID is the raw request value; whitespace-only counts as absent.
func ValidateAccountScope(roles []string, scopeTenantID string) error {
	scope := strings.TrimSpace(scopeTenantID)
	if hasRole(roles, RoleTenantAdmin) {
		if scope == "" {
			return ErrAdminBadRequest
		}
		return nil
	}
	if scope != "" {
		return ErrAdminBadRequest
	}
	return nil
}

// hasRole reports whether the list contains the role, comparing trimmed values
// so a stray space in a stored list is not mistaken for a different role.
func hasRole(roles []string, want string) bool {
	for _, raw := range roles {
		if strings.TrimSpace(raw) == want {
			return true
		}
	}
	return false
}

// roleSet converts a role list into the membership set used for authorisation
// checks, normalising on the way in.
func roleSet(roles []string) map[string]bool {
	normalized := NormalizeAccountRoles(roles)
	out := make(map[string]bool, len(normalized))
	for _, role := range normalized {
		out[role] = true
	}
	return out
}

// hasManagementRole reports whether the role list contains any management role.
func hasManagementRole(roles []string) bool {
	for _, raw := range roles {
		if IsManagementRole(raw) {
			return true
		}
	}
	return false
}

// hasSuperAdmin reports whether the role list grants super administration.
func hasSuperAdmin(roles []string) bool {
	for _, raw := range roles {
		if strings.TrimSpace(raw) == RoleSuperAdmin {
			return true
		}
	}
	return false
}

// publicRegistrationRoles is the only role list public registration may create.
// It is a package-level constant rather than a parameter so no code path can be
// talked into producing something else.
var publicRegistrationRoles = []string{RoleMember}

// maxDisplayNameRunes bounds the display name on every write path. The database
// column is VARCHAR(128); validating here as well means an over-long value is
// refused with a 400 rather than failing as a storage error, and it keeps the
// client from being the only place the rule exists.
const maxDisplayNameRunes = 128
