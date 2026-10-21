package app

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/storage"
	"github.com/google/uuid"
)

// Personal Request Records are read, never written, from the personal console.
// The handlers therefore do one thing the storage layer cannot: turn the
// query string into a filter that can only narrow, and refuse a malformed one
// instead of silently dropping it (a dropped filter looks exactly like a filter
// that matched everything).

const (
	consoleRequestLogsDefaultLimit = 50
	consoleRequestLogsMaxLimit     = 200
)

// handleConsoleRequestLogs serves GET /api/console/request-logs.
//
// The owner comes from the session principal and is bound into the query. A
// `tenant_id`, `owner_user_id` or any other unsupported query parameter is
// ignored: the filter type has no field for it, so a client cannot widen the
// owner-scoped set.
func (s *Server) handleConsoleRequestLogs(w http.ResponseWriter, r *http.Request) {
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
	if s.pg == nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	}

	filter, err := consoleRequestLogFilterFrom(r, principal.UserID)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_filter", err.Error(), reqID)
		return
	}

	items, err := s.pg.ListPersonalRequestLogs(r.Context(), filter)
	if err != nil {
		log.Printf("console request logs: list for %s: %v", principal.UserID, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list request records", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":   items,
		"limit":  filter.Limit,
		"offset": filter.Offset,
	})
}

// handleConsoleRequestLog serves GET /api/console/request-logs/{id}.
//
// A record owned by another Account answers 404, not 403: the endpoint must not
// confirm that an id exists outside the caller's set.
func (s *Server) handleConsoleRequestLog(w http.ResponseWriter, r *http.Request) {
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
	if s.pg == nil {
		writeAdminError(w, reqID, ErrAdminInternal)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/api/console/request-logs/")
	if id == "" || strings.Contains(id, "/") {
		httpx.Error(w, http.StatusNotFound, "not_found", "Request record not found", reqID)
		return
	}

	detail, err := s.pg.PersonalRequestLogDetail(r.Context(), principal.UserID, id)
	if errors.Is(err, storage.ErrPersonalRequestLogNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "Request record not found", reqID)
		return
	}
	if err != nil {
		log.Printf("console request logs: detail %s for %s: %v", id, principal.UserID, err)
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the request record", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": detail})
}

// consoleRequestLogFilterFrom reads the member filters.
//
// Only model, status, key, from, to, limit and offset are honoured. Anything
// else in the query string is deliberately absent from the result type: there
// is no way to express "read someone else's rows" as a filter.
func consoleRequestLogFilterFrom(r *http.Request, ownerID string) (storage.PersonalRequestLogFilter, error) {
	q := r.URL.Query()

	limit := consoleRequestLogsDefaultLimit
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return storage.PersonalRequestLogFilter{}, fmt.Errorf("limit must be a positive integer")
		}
		if n > consoleRequestLogsMaxLimit {
			n = consoleRequestLogsMaxLimit
		}
		limit = n
	}

	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return storage.PersonalRequestLogFilter{}, fmt.Errorf("offset must be a non-negative integer")
		}
		offset = n
	}

	status := 0
	if raw := strings.TrimSpace(q.Get("status")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return storage.PersonalRequestLogFilter{}, fmt.Errorf("status must be a non-negative integer")
		}
		status = n
	}

	keyID := strings.TrimSpace(q.Get("api_key_id"))
	if keyID != "" {
		if _, err := uuid.Parse(keyID); err != nil {
			return storage.PersonalRequestLogFilter{}, fmt.Errorf("api_key_id must be a UUID")
		}
	}

	from, err := parseTimeParam(q.Get("from"))
	if err != nil {
		return storage.PersonalRequestLogFilter{}, err
	}
	to, err := parseTimeParam(q.Get("to"))
	if err != nil {
		return storage.PersonalRequestLogFilter{}, err
	}

	return storage.PersonalRequestLogFilter{
		OwnerUserID: ownerID,
		Limit:       limit,
		Offset:      offset,
		Model:       strings.TrimSpace(q.Get("model")),
		Status:      status,
		APIKeyID:    keyID,
		From:        from,
		To:          to,
	}, nil
}
