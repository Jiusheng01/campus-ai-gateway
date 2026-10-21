package app

import (
	"errors"
	"log"
	"net/http"
	"sort"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/jackc/pgx/v5"
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
	case "/api/console/models":
		s.handleConsoleModels(w, r)
	default:
		httpx.Error(w, http.StatusNotFound, "not_found", "Unknown endpoint", reqID)
	}
}

// consoleAccount re-reads the signed-in Account for a console request.
//
// Tenant membership is not a claim in the access token, and roles or status may
// have changed since it was minted, so every console endpoint resolves the
// account from storage. The helper writes the refusal itself and reports false
// when the caller must stop.
func (s *Server) consoleAccount(w http.ResponseWriter, r *http.Request) (model.AdminUser, bool) {
	reqID := r.Header.Get("X-Request-Id")
	principal, ok := adminPrincipalFrom(r.Context())
	if !ok || principal.UserID == "" {
		writeAdminError(w, reqID, ErrConsoleIdentityUnavailable)
		return model.AdminUser{}, false
	}
	if s.adminAuth == nil {
		writeAdminError(w, reqID, ErrAdminAuthDisabled)
		return model.AdminUser{}, false
	}

	user, err := s.adminAuth.store.FindAdminUserByID(r.Context(), principal.UserID)
	if err != nil {
		if errors.Is(err, storage.ErrAdminUserNotFound) {
			writeAdminError(w, reqID, ErrAdminUserNotFound)
			return model.AdminUser{}, false
		}
		writeAdminError(w, reqID, ErrAdminInternal)
		return model.AdminUser{}, false
	}
	if user.Status != model.AdminStatusEnabled {
		writeAdminError(w, reqID, ErrAdminUserDisabled)
		return model.AdminUser{}, false
	}
	return user, true
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

	user, ok := s.consoleAccount(w, r)
	if !ok {
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

// The catalog's empty states are decisions, not absences. The page can only
// explain why there is nothing to call if the server names the reason, so the
// three cases stay distinct rather than collapsing into an empty list.
const (
	consoleModelsNoTenant         = "no_tenant"
	consoleModelsTenantAllowsNone = "tenant_allows_none"
	consoleModelsNoneEligible     = "no_eligible_models"
)

// consoleModel is the member-visible view of a Virtual Model.
//
// It deliberately carries no Provider, credential, Route or upstream name:
// those are management facts, and a member only needs to know which alias to
// send and how to recognise it. `available` is always true today — the catalog
// only contains statically routable models — but it is stated rather than
// implied so a later change cannot make the page guess.
type consoleModel struct {
	Alias       string `json:"alias"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
}

type consoleModelsResponse struct {
	Models []consoleModel `json:"models"`
	// EmptyReason is one of no_tenant, tenant_allows_none or no_eligible_models
	// when Models is empty, and omitted when there is something to call.
	EmptyReason string `json:"empty_reason,omitempty"`
}

// handleConsoleModels answers GET /api/console/models.
//
// The eligible set is the data plane's own static routing table — enabled
// Virtual Models with at least one enabled Route to an enabled Provider whose
// upstream name is set — intersected with the Tenant's allowlist. Reusing the
// routing table rather than a second SQL definition is what keeps the catalog
// from advertising a model that /v1 would refuse. The Tenant allowlist bounds
// what may be issued on a personal key; it is applied here as a view, never as
// a second admission check.
func (s *Server) handleConsoleModels(w http.ResponseWriter, r *http.Request) {
	writeAuthNoStore(w)
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	user, ok := s.consoleAccount(w, r)
	if !ok {
		return
	}
	if s.pg == nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	}
	if user.TenantID == "" {
		writeJSON(w, http.StatusOK, consoleModelsResponse{
			Models:      []consoleModel{},
			EmptyReason: consoleModelsNoTenant,
		})
		return
	}

	tenant, err := s.pg.TenantByID(r.Context(), user.TenantID)
	if errors.Is(err, pgx.ErrNoRows) {
		// A dangling membership is the same observable state as no membership.
		// It cannot happen through the API (deletion sets tenant_id to NULL),
		// but reporting it as "no Tenant" is honest and keeps the page usable.
		writeJSON(w, http.StatusOK, consoleModelsResponse{
			Models:      []consoleModel{},
			EmptyReason: consoleModelsNoTenant,
		})
		return
	}
	if err != nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	}
	// The Tenant allowlist is turned into the same Identity the data plane uses,
	// so membership is answered by Identity.AllowsModel rather than by a second
	// reading of the JSON list. A nil map on that identity is the empty-list
	// decision, which stays distinct from the wildcard.
	identity := model.IdentityFromAllowedModels(tenant.AllowedModels)
	if !identity.AllModels && identity.AllowedModels == nil {
		writeJSON(w, http.StatusOK, consoleModelsResponse{
			Models:      []consoleModel{},
			EmptyReason: consoleModelsTenantAllowsNone,
		})
		return
	}

	table, err := s.routeTableForModels(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "routing_unavailable",
			"The routing configuration could not be verified", reqID)
		return
	}
	configs, err := s.pg.ListModels(r.Context())
	if err != nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	}
	byAlias := make(map[string]model.ModelConfig, len(configs))
	for _, cfg := range configs {
		if cfg.Status == 1 {
			byAlias[cfg.Alias] = cfg
		}
	}

	aliases := make([]string, 0, len(table.models))
	for alias := range table.models {
		if !identity.AllowsModel(alias) {
			continue
		}
		if _, ok := byAlias[alias]; !ok {
			// The routes were built from this same model set; a missing config
			// means the two reads raced a delete. Omitting the alias is safer
			// than inventing a description for a model we could not read.
			continue
		}
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	models := make([]consoleModel, 0, len(aliases))
	for _, alias := range aliases {
		cfg := byAlias[alias]
		models = append(models, consoleModel{
			Alias:       cfg.Alias,
			DisplayName: cfg.DisplayName,
			Description: cfg.Description,
			Available:   true,
		})
	}
	if len(models) == 0 {
		writeJSON(w, http.StatusOK, consoleModelsResponse{
			Models:      models,
			EmptyReason: consoleModelsNoneEligible,
		})
		return
	}
	writeJSON(w, http.StatusOK, consoleModelsResponse{Models: models})
}
