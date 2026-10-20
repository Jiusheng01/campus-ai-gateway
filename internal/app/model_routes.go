package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
		writeJSON(w, http.StatusOK, map[string]any{"data": items})
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
		s.invalidateRouteTable()
		writeJSON(w, http.StatusCreated, out)
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
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
		if err != nil {
			httpx.Error(w, http.StatusConflict, "model_route_update_failed", "Model route could not be updated", reqID)
			return
		}
		s.invalidateRouteTable()
		writeJSON(w, http.StatusOK, out)
	case http.MethodDelete:
		err := s.pg.DeleteModelRoute(r.Context(), routeID)
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "model_route_not_found", "Model route was not found", reqID)
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to delete model route", reqID)
			return
		}
		s.invalidateRouteTable()
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "PUT, DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func modelIDFromRoutesPath(path string) (string, bool) {
	const prefix = "/admin/models/"
	const suffix = "/routes"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	if !strings.Contains(rest, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(rest, suffix)
	if id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
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
