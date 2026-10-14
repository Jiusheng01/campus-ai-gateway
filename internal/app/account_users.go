package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

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
// administrator.
//
// The one rule that matters here is enforced in storage rather than in this
// function: the system always keeps at least one enabled super administrator.
// It has to live inside the writing transaction, because two concurrent
// demotions would each see the other's administrator and both proceed, leaving
// a gateway nobody can administer. A check here would be a check-then-act race.
//
// Changing your own account is allowed. The invariant above already prevents
// the only genuinely dangerous case, and an operator who deliberately steps
// down from super_admin should be able to: their own sessions are revoked by
// the same change, so the console signs them out immediately afterwards.
func (a *adminAuthService) updateUser(ctx context.Context, targetID string, in userUpdate) (model.AdminUser, error) {
	// Roles are normalised on the way in: unknown roles are dropped and
	// "member" is removed as soon as a management role is present, so
	// ["member","gateway_admin"] cannot be stored and later misread.
	if in.Roles != nil {
		roles := NormalizeAccountRoles(*in.Roles)
		if err := a.store.UpdateAdminUserRoles(ctx, targetID, roles); err != nil {
			switch {
			case errors.Is(err, storage.ErrLastSuperAdmin):
				return model.AdminUser{}, ErrAdminLastSuperAdmin
			case errors.Is(err, storage.ErrAdminUserNotFound):
				return model.AdminUser{}, ErrAdminUserNotFound
			default:
				return model.AdminUser{}, ErrAdminInternal
			}
		}
	}

	if in.Status != nil {
		if err := a.store.SetAdminUserStatus(ctx, targetID, *in.Status); err != nil {
			switch {
			case errors.Is(err, storage.ErrLastSuperAdmin):
				return model.AdminUser{}, ErrAdminLastSuperAdmin
			case errors.Is(err, storage.ErrAdminUserNotFound):
				return model.AdminUser{}, ErrAdminUserNotFound
			default:
				return model.AdminUser{}, ErrAdminInternal
			}
		}
	}

	if in.DisplayName != nil {
		// A label change must not sign anyone out, so this does not touch
		// auth_version.
		if err := a.store.UpdateAdminUserDisplayName(ctx, targetID, *in.DisplayName); err != nil {
			if errors.Is(err, storage.ErrAdminUserNotFound) {
				return model.AdminUser{}, ErrAdminUserNotFound
			}
			return model.AdminUser{}, ErrAdminInternal
		}
	}

	// Role and status changes already bumped auth_version, which is what
	// actually rejects the old tokens. Revoking the sessions as well is what
	// makes the change visible in the user's own device list instead of
	// leaving rows that look live but no longer work. It is best effort: the
	// version bump has already taken effect, so a failure here must not turn a
	// completed change into an error.
	if in.Roles != nil || in.Status != nil {
		if err := a.store.RevokeSessionsForUser(ctx, targetID, revokeReasonRoleChanged); err != nil {
			log.Printf("user management: role or status changed for %s but its sessions could not be revoked: %v", targetID, err)
		}
	}

	updated, err := a.store.FindAdminUserByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			return model.AdminUser{}, ErrAdminUserNotFound
		}
		return model.AdminUser{}, ErrAdminInternal
	}
	return updated, nil
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
	if err := decodeAdminAuthBody(r, &in); err != nil {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}
	if in.Status != nil && *in.Status != model.AdminStatusEnabled && *in.Status != model.AdminStatusDisabled {
		writeAdminError(w, reqID, ErrAdminBadRequest)
		return
	}

	updated, err := s.adminAuth.updateUser(r.Context(), targetID, userUpdate{
		DisplayName: in.DisplayName,
		Roles:       in.Roles,
		Status:      in.Status,
	})
	if err != nil {
		s.auditAdminAuth(r, "user.update_failed", statusForAdminError(err), principal.Username)
		writeAdminError(w, reqID, err)
		return
	}

	// One semantic audit row per change rather than one per HTTP request: the
	// operator reading the trail wants to see "role updated", not "PATCH".
	// The admin middleware only records POST/PUT/DELETE, so PATCH would
	// otherwise leave no trace at all.
	switch {
	case in.Roles != nil:
		s.auditAdminAuth(r, "user.role.update", http.StatusOK, principal.Username)
	case in.Status != nil && *in.Status == model.AdminStatusDisabled:
		s.auditAdminAuth(r, "user.disable", http.StatusOK, principal.Username)
	case in.Status != nil:
		s.auditAdminAuth(r, "user.enable", http.StatusOK, principal.Username)
	default:
		s.auditAdminAuth(r, "user.profile.update", http.StatusOK, principal.Username)
	}

	writeJSON(w, http.StatusOK, adminUserViewFrom(updated))
}
