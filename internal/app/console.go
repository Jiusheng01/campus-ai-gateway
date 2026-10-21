package app

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
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
	// AllowedModels is the Tenant's model allowlist, as stored: ["*"],
	// an explicit sorted list, or [] (the Tenant grants no models). The create
	// drawer uses it as the bound on a key's model scope. It is empty for an
	// unassigned Account, which has no Tenant to draw a ceiling from.
	AllowedModels []string `json:"allowed_models"`
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
	case "/api/console/keys":
		s.handleConsoleKeys(w, r)
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

	// The allowlist is the Tenant's current fact, not a token claim, so it is
	// re-read here. A vanished Tenant answers nil, which is the same projection
	// as an unassigned Account rather than a 500 on a page that would otherwise
	// still be usable.
	allowedModels := make([]string, 0)
	if user.TenantID != "" {
		models, err := s.adminAuth.store.TenantAllowedModels(r.Context(), user.TenantID)
		if err != nil {
			log.Printf("console me: read tenant allowlist for %s: %v", user.TenantID, err)
			writeAdminError(w, reqID, ErrAdminInternal)
			return
		}
		if models != nil {
			allowedModels = models
		}
	}

	writeJSON(w, http.StatusOK, consoleMeResponse{
		AccountID:       user.ID,
		Username:        user.Username,
		DisplayName:     user.DisplayName,
		Roles:           NormalizeAccountRoles(user.Roles),
		Status:          user.Status,
		TenantID:        user.TenantID,
		TenantName:      user.TenantName,
		AllowedModels:   allowedModels,
		WorkspaceUsable: user.TenantID != "",
	})
}

// consoleKeyCreateRequest is the whole of a personal create body.
//
// It has no tenant_id, no owner_user_id and no status field. Decoding into
// this struct is what makes a forged body inert: the fields a personal key
// must not accept simply have nowhere to land, so there is no "ignored field"
// path that a later refactor could accidentally honour.
type consoleKeyCreateRequest struct {
	Name          string     `json:"name"`
	AllowedModels []string   `json:"allowed_models"`
	AllowedIPs    []string   `json:"allowed_ips"`
	ExpireAt      *time.Time `json:"expire_at"`
}

const consoleKeyNameMaxRunes = 128

// handleConsoleKeys serves GET and POST /api/console/keys.
//
// Both operations derive the Account from the session principal. The list
// query binds that id as a predicate, so another Account's keys are not merely
// filtered out of the response — they are never selected.
func (s *Server) handleConsoleKeys(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.UserID == "" {
		writeAdminError(w, reqID, ErrConsoleIdentityUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, err := s.pg.ListAPIKeysByOwner(r.Context(), principal.UserID)
		if err != nil {
			log.Printf("console keys: list for %s: %v", principal.UserID, err)
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list API keys", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items})

	case http.MethodPost:
		s.createConsoleKey(w, r, principal, reqID)

	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) createConsoleKey(w http.ResponseWriter, r *http.Request, principal adminPrincipal, reqID string) {
	var in consoleKeyCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}

	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_api_key_config", "name is required", reqID)
		return
	}
	if utf8.RuneCountInString(in.Name) > consoleKeyNameMaxRunes {
		httpx.Error(w, http.StatusBadRequest, "invalid_api_key_config", "name is too long", reqID)
		return
	}
	if reason := normalizeAllowedModels(&in.AllowedModels); reason != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_api_key_config", reason, reqID)
		return
	}

	plain, prefix, digest, err := security.NewAPIKey(s.cfg.KeyHMACSecret)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "key_generation_failed", "Unable to generate API key", reqID)
		return
	}

	limit := s.cfg.PersonalKeyLimit
	if limit <= 0 {
		limit = personalKeyLimitDefault
	}

	out, err := s.pg.CreatePersonalAPIKey(r.Context(), storage.PersonalKeyCreate{
		OwnerUserID:   principal.UserID,
		Name:          in.Name,
		KeyPrefix:     prefix,
		KeyDigest:     digest,
		AllowedModels: in.AllowedModels,
		AllowedIPs:    in.AllowedIPs,
		ExpireAt:      in.ExpireAt,
		Limit:         limit,
	})
	if err != nil {
		s.writePersonalKeyCreateError(w, reqID, err)
		return
	}

	// The plaintext key appears in this response and nowhere else. The list
	// handler above selects no digest and has no api_key field to return.
	writeJSON(w, http.StatusCreated, model.APIKeyCreateResult{APIKeyAdmin: out, APIKey: plain})
}

// writePersonalKeyCreateError maps the create transaction's refusals onto
// stable codes. Status codes distinguish "your input names a model you may not
// use" (400) from "your account is not currently in a state that can issue a
// key" (409), which the console renders differently.
func (s *Server) writePersonalKeyCreateError(w http.ResponseWriter, reqID string, err error) {
	var limitErr *storage.PersonalKeyLimitError
	var modelErr *storage.PersonalModelNotAllowedError
	switch {
	case errors.Is(err, storage.ErrPersonalTenantUnassigned):
		httpx.Error(w, http.StatusConflict, "tenant_not_assigned",
			"Your account is not assigned to a tenant yet. Ask an administrator to assign one.", reqID)
	case errors.Is(err, storage.ErrPersonalTenantModelsEmpty):
		httpx.Error(w, http.StatusConflict, "tenant_models_empty",
			"Your tenant does not allow any models, so no key can be created.", reqID)
	case errors.As(err, &limitErr):
		writeConsoleError(w, http.StatusConflict, reqID, "key_limit_reached",
			"You have reached the maximum number of API keys for your account.", map[string]any{"limit": limitErr.Limit})
	case errors.As(err, &modelErr):
		writeConsoleError(w, http.StatusBadRequest, reqID, "model_not_allowed",
			"One of the requested models is not allowed by your tenant.", map[string]any{"model": modelErr.Model})
	case errors.Is(err, storage.ErrPersonalAccountNotFound):
		httpx.Error(w, http.StatusNotFound, "account_not_found", "Account was not found", reqID)
	default:
		log.Printf("console keys: create for %s: %v", reqID, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "API key could not be created", reqID)
	}
}

// writeConsoleError is httpx.Error with extra machine-readable fields. The
// console renders `limit` and `model` instead of parsing prose, so a wording
// change cannot break the message.
func writeConsoleError(w http.ResponseWriter, status int, reqID, code, message string, details map[string]any) {
	body := map[string]any{"code": code, "message": message, "request_id": reqID}
	for k, v := range details {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": body})
}
