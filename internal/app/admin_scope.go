package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Tenant scoping for administrators.
//
// A tenant_admin is an administrator whose authority is bounded to one subtree
// of the tenant tree. The RBAC allow-list in adminPrincipal.can decides *which
// endpoints* such an account may address; this file decides *which rows*.
//
// The two checks are deliberately separate. Doing the whole job in can() would
// mean encoding row identity into a URL pattern, and doing it only in the
// handlers would leave the endpoints themselves reachable by a role that has no
// business calling them.
//
// Every scoped read narrows in the query (path prefix), and every scoped write
// checks the row it is about to change. A filter applied after the fact would
// still ship the other departments' rows to the client, and one forgotten
// filter would display them.

// adminScopeTenantID returns the subtree the caller is confined to, or "" when
// their authority is global.
func adminScopeTenantID(r *http.Request) string {
	p, ok := adminPrincipalFrom(r.Context())
	if !ok || p.ScopeTenantID == nil {
		return ""
	}
	return strings.TrimSpace(*p.ScopeTenantID)
}

// tenantInScope reports whether tenantID falls inside the caller's subtree.
//
// A global administrator is always in scope. A scoped one is checked against
// the stored materialized path, which is the same test the tenant list uses —
// so "inside my subtree" cannot mean one thing in a list and another in a
// write.
//
// A malformed id is out of scope rather than an error: the database cast would
// otherwise turn a bad request into a 500.
func (s *Server) tenantInScope(ctx context.Context, scope, tenantID string, includeSelf bool) (bool, error) {
	if scope == "" {
		return true, nil
	}
	if tenantID == "" {
		return false, nil
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return false, nil
	}
	return s.pg.TenantIsDescendant(ctx, scope, tenantID, includeSelf)
}

// requireTenantInScope answers 403 and returns false when the tenant is outside
// the caller's subtree.
//
// It reports a storage failure as 500 rather than folding it into "forbidden":
// an unreachable database must not look like a permission decision, because the
// caller would stop asking and the operator would never learn the check was
// never made.
func (s *Server) requireTenantInScope(w http.ResponseWriter, r *http.Request, tenantID, reqID string) bool {
	scope := adminScopeTenantID(r)
	if scope == "" {
		return true
	}
	ok, err := s.tenantInScope(r.Context(), scope, tenantID, true)
	if err != nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return false
	}
	if !ok {
		writeAdminError(w, reqID, ErrAdminTenantOutOfScope)
		return false
	}
	return true
}

// requireTenantWritable is requireTenantInScope for changes to a tenant's own
// allocation.
//
// A scoped administrator may read their own tenant but may not rewrite its
// budget: the limits above them were granted by someone else, and raising your
// own ceiling is the one edit that makes the whole hierarchy decorative. They
// may still edit their descendants, which is what delegating a department's
// quota means.
func (s *Server) requireTenantWritable(w http.ResponseWriter, r *http.Request, tenantID, reqID string) bool {
	scope := adminScopeTenantID(r)
	if scope == "" {
		return true
	}
	ok, err := s.tenantInScope(r.Context(), scope, tenantID, false)
	if err != nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return false
	}
	if !ok {
		writeAdminError(w, reqID, ErrAdminTenantOutOfScope)
		return false
	}
	return true
}

// requireAPIKeyInScope checks that the key named in the URL belongs to a tenant
// inside the caller's subtree.
//
// The lookup is against the stored row, never the request body: the body is
// what the caller is trying to change, and trusting it would let a scoped
// administrator move someone else's key into their own subtree by editing the
// tenant_id field.
func (s *Server) requireAPIKeyInScope(w http.ResponseWriter, r *http.Request, keyID, reqID string) bool {
	scope := adminScopeTenantID(r)
	if scope == "" {
		return true
	}
	if _, err := uuid.Parse(keyID); err != nil {
		// Not a UUID cannot name a row; the handlers answer 404 for the same
		// input, so keep that answer rather than inventing a 403 here.
		return true
	}
	tenantID, err := s.pg.APIKeyTenantID(r.Context(), keyID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Let the handler produce its own 404.
			return true
		}
		writeAdminError(w, reqID, ErrAdminInternal)
		return false
	}
	ok, err := s.tenantInScope(r.Context(), scope, tenantID, true)
	if err != nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return false
	}
	if !ok {
		writeAdminError(w, reqID, ErrAdminTenantOutOfScope)
		return false
	}
	return true
}
