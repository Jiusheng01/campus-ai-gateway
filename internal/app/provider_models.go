package app

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// providerModelRefreshTimeoutMS bounds the upstream /models call. It is a
// management request, so it gets the management-plane window rather than the
// data plane's chat timeout, and the same value is the transport's header
// deadline.
const providerModelRefreshTimeoutMS = 10000

// maxUpstreamModelListBytes caps how much of the upstream list the gateway
// buffers. Management JSON is limited to 1 MiB (ADR-0012).
const maxUpstreamModelListBytes = 1 << 20

// providerModelsPath is appended to the channel's configured base URL to read
// its model list. The base already ends in /v1, /api/v3 or /api/paas/v4, and
// directTargetURL leaves a path that does not start with /v1 untouched, so
// naming /v1/models here would ask those channels for /api/v3/v1/models.
const providerModelsPath = "/models"

// The Provider Model list is what a channel says it can serve. It is managed
// per channel — /admin/providers/{id}/models — because the list belongs to the
// channel's credential boundary: two channels for the same supplier may expose
// different models.

// providerIDFromProviderPath parses /admin/providers/{id}{suffix}, where the
// suffix is the fixed tail after the provider id. The id has to be a single
// UUID segment: a non-UUID cannot name a row, and the database cast would fail
// the whole statement.
func providerIDFromProviderPath(path, suffix string) (string, bool) {
	rest := strings.TrimPrefix(path, "/admin/providers/")
	if rest == path || !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	id := strings.TrimSuffix(rest, suffix)
	if id == "" || strings.Contains(id, "/") || !isUUID(id) {
		return "", false
	}
	return id, true
}

func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// providerIDFromModelsPath parses /admin/providers/{id}/models.
func providerIDFromModelsPath(path string) (string, bool) {
	return providerIDFromProviderPath(path, "/models")
}

// providerIDFromModelsRefreshPath parses
// /admin/providers/{id}/models/refresh.
func providerIDFromModelsRefreshPath(path string) (string, bool) {
	return providerIDFromProviderPath(path, "/models/refresh")
}

// providerModelIDsFromPath parses
// /admin/providers/{providerID}/models/{modelID}.
func providerModelIDsFromPath(path string) (providerID, modelID string, ok bool) {
	rest := strings.TrimPrefix(path, "/admin/providers/")
	if rest == path {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != "models" || !isUUID(parts[0]) || !isUUID(parts[2]) {
		return "", "", false
	}
	return parts[0], parts[2], true
}

// handleProviderModels serves GET and POST /admin/providers/{id}/models.
//
// GET is the channel's list with each name's source. POST adds one name by
// hand; a name that is already listed is returned unchanged rather than
// duplicated or relabelled, because the source records where it came from.
func (s *Server) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	providerID, ok := providerIDFromModelsPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.listProviderModels(w, r, providerID, reqID)
	case http.MethodPost:
		s.addProviderModel(w, r, providerID, reqID)
	default:
		w.Header().Set("Allow", "GET, POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
	}
}

func (s *Server) listProviderModels(w http.ResponseWriter, r *http.Request, providerID, reqID string) {
	items, err := s.pg.ListProviderModels(r.Context(), providerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list provider models", reqID)
		return
	}
	if items == nil {
		items = []model.ProviderModel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) addProviderModel(w http.ResponseWriter, r *http.Request, providerID, reqID string) {
	var in struct {
		UpstreamModel string `json:"upstream_model"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	if reason := validateProviderModelName(in.UpstreamModel); reason != "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_provider_model", reason, reqID)
		return
	}
	if _, err := s.pg.ProviderByID(r.Context(), providerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the provider", reqID)
		return
	}

	item, created, err := s.pg.AddProviderModel(r.Context(), providerID, in.UpstreamModel)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to add the provider model", reqID)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, item)
}

// handleProviderModelByID serves DELETE
// /admin/providers/{providerID}/models/{modelID}.
func (s *Server) handleProviderModelByID(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	providerID, modelID, ok := providerModelIDsFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		w.Header().Set("Allow", "DELETE")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}
	if err := s.pg.DeleteProviderModel(r.Context(), providerID, modelID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "provider_model_not_found", "Provider model was not found", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to delete the provider model", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": modelID})
}

// validateProviderModelName checks the shape of a manual name. The column is
// VARCHAR(128), so the limit is characters rather than bytes; an empty or blank
// name would be a row the console cannot show.
func validateProviderModelName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "upstream_model is required"
	}
	if utf8.RuneCountInString(strings.TrimSpace(name)) > 128 {
		return "upstream_model must be at most 128 characters"
	}
	return ""
}

// handleProviderModelsRefresh merges the models the provider itself reports.
//
// It reuses the admin-triggered, gateway-to-provider outbound path — the same
// credential resolution and transport as the model connectivity test — with a
// fixed GET /models target appended to the channel's base URL. Nothing from
// the admin request (headers, query, body) is forwarded, so the refresh cannot
// be pointed anywhere the channel is not already configured to go.
//
// The merge happens only after the upstream answered and its list parsed. A
// failure at any earlier point leaves the existing list untouched, and the
// merge itself is additive: it never deletes a manual row and never rewrites
// the source of a name that is already listed.
func (s *Server) handleProviderModelsRefresh(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	providerID, ok := providerIDFromModelsRefreshPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	provider, err := s.pg.ProviderByID(r.Context(), providerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.Error(w, http.StatusNotFound, "provider_not_found", "Provider was not found", reqID)
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the provider", reqID)
		return
	}

	route := directRoute{
		Provider:             provider.Name,
		BaseURL:              provider.BaseURL,
		CredentialEnv:        provider.CredentialEnv,
		CredentialSource:     provider.CredentialSource,
		CredentialRef:        provider.CredentialSecretRef,
		CredentialCiphertext: provider.CredentialCiphertext,
		TimeoutMS:            providerModelRefreshTimeoutMS,
	}
	probe, err := http.NewRequestWithContext(r.Context(), http.MethodGet, providerModelsPath, nil)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "refresh_request_failed", "Unable to create the refresh request", reqID)
		return
	}

	resp, err := s.attemptDirect(probe, nil, requestEnvelope{}, route)
	if err != nil {
		// A denied target is a policy decision with its own reason; surfacing
		// it as a credential failure would send the operator to the wrong fix.
		// It goes through the same security-event path as a data-plane denial,
		// because the refresh button is another way to aim a channel at the
		// gateway's own network. The log and the event keep the full refusal;
		// the response gets the address-free summary.
		var denied *egressDeniedError
		if errors.As(err, &denied) {
			log.Printf("provider models refresh: request %s channel %s: %s", reqID, oneLine(provider.Name), denied.Error())
			s.recordEgressDenial(r, reqID, "", err)
			writeEgressDenied(w, reqID, denied)
			return
		}
		// A missing credential is the operator's fix and names the variable,
		// never its value. Transport errors can carry the dialled target, so
		// only a fixed phrase leaves the gateway.
		if detail := clientSafeFailure(err); detail != "" {
			httpx.Error(w, http.StatusBadRequest, "credential_unavailable",
				"cannot refresh models from channel "+provider.Name+": "+detail, reqID)
			return
		}
		log.Printf("provider models refresh: request %s channel %s: %s", reqID, oneLine(provider.Name), directFailureReason(err))
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable",
			"cannot refresh models from channel "+provider.Name+": "+directFailureReason(err), reqID)
		return
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamModelListBytes))
	_ = resp.Body.Close()
	if readErr != nil {
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable",
			"cannot refresh models from channel "+provider.Name+": the response could not be read", reqID)
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("provider models refresh: request %s channel %s: upstream status %d", reqID, oneLine(provider.Name), resp.StatusCode)
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable",
			"cannot refresh models from channel "+provider.Name+": the channel answered HTTP "+strconv.Itoa(resp.StatusCode)+" for GET /models", reqID)
		return
	}

	names, err := parseUpstreamModelIDs(body)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable",
			"cannot refresh models from channel "+provider.Name+": the model list could not be read", reqID)
		return
	}

	added, err := s.pg.MergeFetchedProviderModels(r.Context(), provider.ID, names)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to merge the provider models", reqID)
		return
	}
	items, err := s.pg.ListProviderModels(r.Context(), provider.ID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to list provider models", reqID)
		return
	}
	if items == nil {
		items = []model.ProviderModel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items, "added": added})
}

// parseUpstreamModelIDs reads an OpenAI-compatible model list: data[].id. Empty
// and repeated identifiers are dropped; a body without a data array is not a
// model list and fails the refresh rather than merging nothing silently.
func parseUpstreamModelIDs(body []byte) ([]string, error) {
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Data == nil {
		return nil, errors.New("the response has no data array")
	}
	seen := make(map[string]struct{}, len(payload.Data))
	names := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		names = append(names, id)
	}
	return names, nil
}
