package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// handleAllModelRoutes serves the aggregate route policy read. It is a
// management projection over gateway_model_route, not another routing source.
func (s *Server) handleAllModelRoutes(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	q := r.URL.Query()
	status, _ := strconv.Atoi(q.Get("status"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	items, err := s.pg.ListRouteViews(r.Context(), storage.RouteViewFilter{
		Model: q.Get("model"), Provider: q.Get("provider"), Status: status, Limit: limit, Offset: offset,
	})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list routes", reqID)
		return
	}
	if items == nil {
		items = []model.RouteView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "limit": limit, "offset": offset})
}

func (s *Server) handleModelRoutes(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	modelID, ok := modelIDFromRoutesPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := uuid.Parse(modelID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_model_id", "model_id must be a valid UUID", reqID)
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, err := s.pg.ListModelRoutes(r.Context(), modelID)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list model routes", reqID)
			return
		}
		m, err := s.pg.ModelByID(r.Context(), modelID)
		if err != nil {
			httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": items, "version": m.ConfigVersion})
	case http.MethodPut:
		s.replaceModelRoutes(w, r, modelID, reqID)
	case http.MethodPost:
		var in model.ModelRoute
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		in.ModelID = modelID
		if err := validateModelRoute(&in); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_model_route", err, reqID)
			return
		}
		out, err := s.pg.CreateModelRoute(r.Context(), modelID, in)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusConflict, "model_route_create_failed", "Model route could not be created", reqID)
			return
		}
		writeJSON(w, http.StatusCreated, out)
	default:
		w.Header().Set("Allow", "GET, POST, PUT")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

type routeSetInput struct {
	ExpectedVersion int64        `json:"expected_version"`
	Routes          []routeInput `json:"routes"`
}

type routeInput struct {
	ID            string `json:"id"`
	ProviderID    string `json:"provider_id"`
	UpstreamModel string `json:"upstream_model"`
	Priority      int    `json:"priority"`
	Weight        int    `json:"weight"`
	TimeoutMS     int    `json:"timeout_ms"`
	Status        int    `json:"status"`
}

// replaceModelRoutes validates and applies a whole route set.
//
// Validation happens before any write. The version check and the write happen
// under the storage transaction's model-row lock, so a stale editor gets 409
// instead of silently overwriting another administrator.
func (s *Server) replaceModelRoutes(w http.ResponseWriter, r *http.Request, modelID, reqID string) {
	var in routeSetInput
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON with expected_version and routes", reqID)
		return
	}
	if in.ExpectedVersion <= 0 {
		httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "expected_version is required", reqID)
		return
	}
	if len(in.Routes) == 0 || len(in.Routes) > 50 {
		httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "routes must contain between 1 and 50 entries", reqID)
		return
	}
	providers, err := s.pg.ListProviders(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to validate route providers", reqID)
		return
	}
	knownProviders := make(map[string]bool, len(providers))
	for _, p := range providers {
		knownProviders[p.ID] = true
	}
	existing, err := s.pg.ListModelRoutes(r.Context(), modelID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to validate the route set", reqID)
		return
	}
	existingByID := map[string]model.ModelRoute{}
	existingByBinding := map[string]model.ModelRoute{}
	for _, r := range existing {
		existingByID[r.ID] = r
		existingByBinding[r.ProviderID+"\x00"+r.UpstreamModel] = r
	}
	seen := map[string]bool{}
	out := make([]model.ModelRoute, 0, len(in.Routes))
	enabled := 0
	for _, raw := range in.Routes {
		raw.ProviderID = strings.TrimSpace(raw.ProviderID)
		raw.UpstreamModel = strings.TrimSpace(raw.UpstreamModel)
		if _, err := uuid.Parse(raw.ProviderID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "provider_id must be a valid UUID", reqID)
			return
		}
		if !knownProviders[raw.ProviderID] {
			httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "a route references a channel that does not exist", reqID)
			return
		}
		if raw.UpstreamModel == "" || len([]rune(raw.UpstreamModel)) > 128 {
			httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "upstream_model is required and must be at most 128 characters", reqID)
			return
		}
		if raw.Priority < 1 || raw.Priority > 1000 || raw.Weight < 0 || raw.Weight > 10000 || raw.TimeoutMS < 1 || raw.TimeoutMS > 600000 || (raw.Status != 1 && raw.Status != 2) {
			httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "priority, weight, timeout_ms or status is outside its allowed range", reqID)
			return
		}
		binding := raw.ProviderID + "\x00" + raw.UpstreamModel
		if seen[binding] {
			httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "duplicate provider_id/upstream_model binding", reqID)
			return
		}
		seen[binding] = true
		if raw.ID != "" && existingByID[raw.ID].ID == "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "a route id does not belong to this model", reqID)
			return
		}
		var prior model.ModelRoute
		if raw.ID != "" {
			prior = existingByID[raw.ID]
		} else {
			prior = existingByBinding[binding]
		}
		unchanged := prior.ID != "" &&
			prior.ProviderID == raw.ProviderID && prior.UpstreamModel == raw.UpstreamModel &&
			prior.Priority == raw.Priority && prior.Weight == raw.Weight &&
			prior.TimeoutMS == raw.TimeoutMS && prior.Status == raw.Status
		if raw.Status == 1 {
			enabled++
			if raw.Weight <= 0 && !unchanged {
				httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "an enabled line that is new or changed must have weight greater than 0", reqID)
				return
			}
		}
		out = append(out, model.ModelRoute{
			ID: raw.ID, ModelID: modelID, ProviderID: raw.ProviderID,
			UpstreamModel: raw.UpstreamModel, Priority: raw.Priority,
			Weight: raw.Weight, TimeoutMS: raw.TimeoutMS, Status: raw.Status,
		})
	}
	m, err := s.pg.ModelByID(r.Context(), modelID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "model_not_found", "Model was not found", reqID)
		return
	}
	if m.Status == 1 && enabled == 0 {
		httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "an enabled model must keep at least one enabled line", reqID)
		return
	}

	updated, routes, err := s.pg.ReplaceModelRoutes(r.Context(), modelID, in.ExpectedVersion, out)
	switch {
	case errors.Is(err, storage.ErrConfigVersionConflict):
		current, _ := s.pg.ModelByID(r.Context(), modelID)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":            "config_version_conflict",
				"message":         "The route set changed since it was loaded; current version is " + strconv.FormatInt(current.ConfigVersion, 10),
				"request_id":      reqID,
				"current_version": current.ConfigVersion,
			},
		})
		return
	case errors.Is(err, storage.ErrLastModelRoute), errors.Is(err, storage.ErrLastEnabledModelRoute):
		httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "the model must keep a route and an enabled model must keep an enabled route", reqID)
		return
	case err != nil:
		httpx.Error(w, http.StatusBadRequest, "invalid_route_set", "The route set could not be saved", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": updated.ConfigVersion, "routes": routes})
}

func (s *Server) handleModelRouteByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	routeID := strings.TrimPrefix(r.URL.Path, "/admin/model-routes/")
	if routeID == "" || strings.Contains(routeID, "/") {
		http.NotFound(w, r)
		return
	}
	if _, err := uuid.Parse(routeID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_model_route_id", "route_id must be a valid UUID", reqID)
		return
	}

	switch r.Method {
	case http.MethodPut:
		var in model.ModelRoute
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
			return
		}
		if err := validateModelRoute(&in); err != "" {
			httpx.Error(w, http.StatusBadRequest, "invalid_model_route", err, reqID)
			return
		}
		out, err := s.pg.UpdateModelRoute(r.Context(), routeID, in)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "model_route_not_found", "Model route was not found", reqID)
			return
		}
		if errors.Is(err, storage.ErrLastEnabledModelRoute) {
			httpx.Error(w, http.StatusConflict, "last_enabled_route", "The last enabled route cannot be disabled", reqID)
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusConflict, "model_route_update_failed", "Model route could not be updated", reqID)
			return
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodDelete:
		err := s.pg.DeleteModelRoute(r.Context(), routeID)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "model_route_not_found", "Model route was not found", reqID)
			return
		}
		if errors.Is(err, storage.ErrLastModelRoute) {
			httpx.Error(w, http.StatusConflict, "last_model_route", "The last route of a model cannot be deleted", reqID)
			return
		}
		if errors.Is(err, storage.ErrLastEnabledModelRoute) {
			httpx.Error(w, http.StatusConflict, "last_enabled_route", "The last enabled route cannot be deleted", reqID)
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to delete model route", reqID)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

// modelIDFromPath parses /admin/models/{uuid}{suffix}. One helper keeps every
// model subresource (routes, impact, runtime-summary, authorizations) agreeing
// on what a model id can look like.
func modelIDFromPath(path, suffix string) (string, bool) {
	const prefix = "/admin/models/"
	rest := strings.TrimPrefix(path, prefix)
	if rest == path || !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(rest, suffix)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

func modelIDFromRoutesPath(path string) (string, bool) {
	return modelIDFromPath(path, "/routes")
}

func validateModelRoute(v *model.ModelRoute) string {
	v.ProviderID = strings.TrimSpace(v.ProviderID)
	v.UpstreamModel = strings.TrimSpace(v.UpstreamModel)
	if v.ProviderID == "" || v.UpstreamModel == "" {
		return "provider_id and upstream_model are required"
	}
	if _, err := uuid.Parse(v.ProviderID); err != nil {
		return "provider_id must be a valid UUID"
	}
	if v.Priority == 0 {
		v.Priority = 1
	}
	if v.Weight == 0 {
		v.Weight = 100
	}
	if v.TimeoutMS == 0 {
		v.TimeoutMS = 60000
	}
	if v.Status == 0 {
		v.Status = 1
	}
	if v.Priority < 1 {
		return "priority must be at least 1"
	}
	if v.Weight < 0 {
		return "weight cannot be negative"
	}
	if v.TimeoutMS <= 0 {
		return "timeout_ms must be greater than 0"
	}
	if v.Status != 1 && v.Status != 2 {
		return "status must be 1 (enabled) or 2 (disabled)"
	}
	return ""
}
