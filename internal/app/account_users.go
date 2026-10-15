package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// adminUserView is the safe projection of an account for the user management
// page.
//
// It deliberately has no password_hash, no refresh hash and no auth_version.
// The console needs to show who an account is and what it may do; nothing about
// how it authenticates belongs in a response that a browser will hold in memory
// and a proxy may log.
type adminUserView struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Status      int      `json:"status"`
	LastLoginAt *string  `json:"last_login_at"`
	CreatedAt   string   `json:"created_at"`
}

func adminUserViewFrom(u model.AdminUser) adminUserView {
	var lastLogin *string
	if u.LastLoginAt != nil {
		formatted := u.LastLoginAt.UTC().Format(time.RFC3339)
		lastLogin = &formatted
	}
	return adminUserView{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Roles:       NormalizeAccountRoles(u.Roles),
		Status:      u.Status,
		LastLoginAt: lastLogin,
		CreatedAt:   u.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// --- service ---------------------------------------------------------------

// userUpdate is a partial change. A nil field means "leave it alone", which is
// what lets the UI send only what the operator actually touched.
type userUpdate struct {
	DisplayName *string
	Roles       *[]string
	Status      *int
}

// listUsers returns every account plus the summary counts.
func (a *adminAuthService) listUsers(ctx context.Context) ([]model.AdminUser, model.AdminUserStats, error) {
	users, err := a.store.ListAdminUsers(ctx, 500)
	if err != nil {
		return nil, model.AdminUserStats{}, ErrAdminInternal
	}
	stats, err := a.store.AdminUserStats(ctx)
	if err != nil {
		return nil, model.AdminUserStats{}, ErrAdminInternal
	}
	return users, stats, nil
}

// updateUser applies a role, status or display-name change on behalf of a super
// administrator, and reports what actually changed.
//
// The change is one storage call, and therefore one transaction. Applying roles,
// status and display name as three separate mutations meant a patch that was
// rejected for the super-administrator invariant could still leave the display
// name written, and a patch that touched roles and status together bumped
// auth_version twice for what is one logical change.
//
// Validation happens here rather than in storage because it produces an HTTP
// 400, and storage should not be deciding request semantics. Normalisation is
// idempotent, so running it on this side of the boundary changes nothing about
// what is stored.
//
// Changing your own account is allowed. The invariant already prevents the only
// genuinely dangerous case, and an operator who deliberately steps down from
// super_admin should be able to: their own sessions are revoked by the same
// change, so the console signs them out immediately afterwards.
func (a *adminAuthService) updateUser(ctx context.Context, targetID string, in userUpdate) (model.AdminUser, storage.AdminUserChangeSet, error) {
	mutation := storage.AdminUserMutation{}

	if in.Roles != nil {
		// Unknown roles are refused rather than dropped: an operator who sends
		// ["gateway_admin","god_mode"] should be told the request was not
		// understood, not handed an apparent success that quietly discarded
		// half of it.
		if err := ValidateAccountRoles(*in.Roles); err != nil {
			return model.AdminUser{}, storage.AdminUserChangeSet{}, err
		}
		roles := NormalizeAccountRoles(*in.Roles)
		mutation.Roles = &roles
	}

	if in.Status != nil {
		if *in.Status != model.AdminStatusEnabled && *in.Status != model.AdminStatusDisabled {
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminBadRequest
		}
		status := *in.Status
		mutation.Status = &status
	}

	if in.DisplayName != nil {
		name := strings.TrimSpace(*in.DisplayName)
		if utf8.RuneCountInString(name) > maxDisplayNameRunes {
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminBadRequest
		}
		mutation.DisplayName = &name
	}

	updated, changes, err := a.store.UpdateAdminUserAccount(ctx, targetID, mutation)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrLastSuperAdmin):
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminLastSuperAdmin
		case errors.Is(err, storage.ErrAdminUserNotFound):
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminUserNotFound
		default:
			return model.AdminUser{}, storage.AdminUserChangeSet{}, ErrAdminInternal
		}
	}

	// A display-name-only edit is not an authentication event: nobody is signed
	// out and no token is invalidated.
	if !changes.AuthenticationChanged() {
		return updated, changes, nil
	}

	// Exactly one revocation per logical mutation. Revoking after each field
	// would do the same work twice for a combined patch and would leave the
	// second reason in the audit trail as though it were a separate event.
	//
	// This is best effort: the transaction has already bumped auth_version, so
	// every token issued before the change is rejected on its next use whether
	// or not the session rows were tidied up. Failing here must not undo a
	// permission change that has already committed.
	if err := a.store.RevokeSessionsForUser(ctx, targetID, revokeReasonForChange(changes)); err != nil {
		log.Printf("user management: account %s changed but its sessions could not be revoked: %v", targetID, err)
	}
	return updated, changes, nil
}

// revokeReasonForChange names the reason recorded on the revoked sessions, so
// the device list and the audit trail say what actually happened rather than
// lumping every kind of change under one label.
func revokeReasonForChange(c storage.AdminUserChangeSet) string {
	switch {
	case c.RolesChanged && c.StatusChanged:
		return revokeReasonSecurityChanged
	case c.RolesChanged:
		return revokeReasonRoleChanged
	case c.NewStatus == model.AdminStatusDisabled:
		return revokeReasonDisabled
	default:
		return revokeReasonStatusChanged
	}
}

// --- handlers --------------------------------------------------------------

// handleAdminUsers serves GET /admin/users.
//
// Only super_admin reaches this handler: the RBAC allow-list in adminPrincipal
// grants no other role the /admin/users prefix. That matters more here than
// anywhere else — gateway_admin having user management would be able to promote
// itself to super_admin, which is the whole reason the prefix is not shared.
//
// The mode check matters as much as the role check: in token and trusted-header
// mode there is no account service behind this server, so reaching for it would
// panic rather than refuse.
func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	users, stats, err := s.adminAuth.listUsers(r.Context())
	if err != nil {
		writeAdminError(w, reqID, err)
		return
	}
	views := make([]adminUserView, 0, len(users))
	for _, u := range users {
		views = append(views, adminUserViewFrom(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": views,
		"stats": map[string]int64{
			"total":    stats.Total,
			"members":  stats.Members,
			"admins":   stats.Admins,
			"disabled": stats.Disabled,
		},
	})
}

type adminUserUpdateRequest struct {
	DisplayName *string   `json:"display_name"`
	Roles       *[]string `json:"roles"`
	Status      *int      `json:"status"`
}

// handleAdminUserByID serves PATCH /admin/users/{id}.
func (s *Server) handleAdminUserByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	// Same guard as the list handler: without an account service there is
	// nothing to update, and the request must be refused rather than dereference
	// a nil service.
	if !s.cfg.SessionAuthEnabled() || s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.UserID == "" {
		writeAdminError(w, reqID, ErrAdminForbidden)
		return
	}

	targetID := strings.TrimPrefix(r.URL.Path, "/admin/users/")
	if targetID == "" || strings.Contains(targetID, "/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPatch {
		w.Header().Set("Allow", "PATCH")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	var in adminUserUpdateRequest
	if err := decodeAdminAuthBody(w, r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	updated, changes, err := s.adminAuth.updateUser(r.Context(), targetID, userUpdate{
		DisplayName: in.DisplayName,
		Roles:       in.Roles,
		Status:      in.Status,
	})
	if err != nil {
		s.auditAdminAuth(r, "user.update_failed", statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}

	// The audit trail follows what changed, not which fields were present in the
	// request. "The client sent a roles field" and "the roles changed" are
	// different facts: a console that resends the current values would otherwise
	// fill the trail with events that never happened.
	for _, action := range accountAuditActions(changes) {
		s.auditAdminAuth(r, action, http.StatusOK, principal.Username)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"data": adminUserViewFrom(updated),
		// The client is told which fields actually moved so it can describe the
		// outcome accurately instead of guessing from what it sent.
		"changes": map[string]bool{
			"roles":          changes.RolesChanged,
			"status":         changes.StatusChanged,
			"display_name":   changes.DisplayNameChanged,
			"authentication": changes.AuthenticationChanged(),
		},
	})
}

// accountAuditActions lists the semantic events a change set produced, in a
// stable order.
//
// It returns a list rather than a single action because one request can be
// several events an operator later wants to find independently: a patch that
// demotes an account and disables it is both a role update and a disable, and
// collapsing that into one row would hide half of what happened.
//
// A no-op returns nothing at all. Recording "role updated" because the request
// happened to contain a roles field would put events in the trail that never
// took place.
func accountAuditActions(changes storage.AdminUserChangeSet) []string {
	actions := make([]string, 0, 3)
	if changes.RolesChanged {
		actions = append(actions, "user.role.update")
	}
	if changes.StatusChanged {
		if changes.NewStatus == model.AdminStatusDisabled {
			actions = append(actions, "user.disable")
		} else {
			actions = append(actions, "user.enable")
		}
	}
	if changes.DisplayNameChanged {
		actions = append(actions, "user.profile.update")
	}
	return actions
}
