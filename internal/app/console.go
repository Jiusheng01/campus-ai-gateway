package app

import (
	"errors"
	"log"
	"net/http"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
)

// consoleMiddleware is the boundary of the personal workspace.
//
// It deliberately reuses the existing control-plane authentication rather than
// inventing a second credential path: /api/console/* accepts the same access
// token as /admin/*, including the existing disabled-account check. What it adds
// is the one thing the personal workspace cannot work without — a stable account
// id. trusted-header mode (and the break-glass ADMIN_TOKEN) authenticate a
// username, not an account, so they are refused with a distinct code instead of
// being treated as an empty owner.
//
// This ticket authorises by identity only. Per-resource ownership checks belong
// to the endpoints that read resources, starting with the keys API.
func (s *Server) consoleMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		principal, err := s.authenticateAdmin(r)
		if err != nil {
			if errors.Is(err, ErrAdminInternal) {
				log.Printf("console auth: internal failure on %s: %v", r.URL.Path, err)
			}
			writeAdminError(w, reqID, err)
			return
		}
		if principal.UserID == "" {
			writeAdminError(w, reqID, ErrConsoleIdentityUnavailable)
			return
		}
		next.ServeHTTP(w, r.WithContext(withAdminPrincipal(r.Context(), principal)))
	})
}

// consoleMeResponse is the safe projection of the signed-in account for the
// personal workspace. It carries no session id and no credential material:
// the browser only needs to know who it is and whether a Tenant has been
// assigned.
type consoleMeResponse struct {
	AccountID   string   `json:"account_id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
	Status      int      `json:"status"`
	// TenantID is empty when the Account has no Tenant Membership. The name is
	// projected for display; the id is the identifier.
	TenantID   string `json:"tenant_id"`
	TenantName string `json:"tenant_name"`
	// WorkspaceUsable reports whether the Account has the Tenant it needs to
	// spend against. It is false for an unassigned Account, which is an honest
	// state rather than an error.
	WorkspaceUsable bool `json:"workspace_usable"`
}

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	switch r.URL.Path {
	case "/api/console/me":
		s.handleConsoleMe(w, r)
	default:
		httpx.Error(w, http.StatusNotFound, "not_found", "Unknown endpoint", reqID)
	}
}

// handleConsoleMe answers GET /api/console/me.
//
// The account is re-read from the store rather than reconstructed from the
// access token: Tenant membership is not a claim, and roles or status may have
// changed since the token was minted.
func (s *Server) handleConsoleMe(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.UserID == "" {
		writeAdminError(w, reqID, ErrConsoleIdentityUnavailable)
		return
	}
	if s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return
	}

	user, err := s.adminAuth.store.FindAdminUserByID(r.Context(), principal.UserID)
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			writeAdminError(w, reqID, ErrAdminUserNotFound)
			return
		}
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	}
	if user.Status != model.AdminStatusEnabled {
		writeAdminError(w, reqID, ErrAdminUserDisabled)
		return
	}

	writeJSON(w, http.StatusOK, consoleMeResponse{
		AccountID:       user.ID,
		Username:        user.Username,
		DisplayName:     user.DisplayName,
		Roles:           NormalizeAccountRoles(user.Roles),
		Status:          user.Status,
		TenantID:        user.TenantID,
		TenantName:      user.TenantName,
		WorkspaceUsable: user.TenantID != "",
	})
}
