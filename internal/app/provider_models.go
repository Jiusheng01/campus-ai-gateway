package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The upstream model list is a configuration aid, not a health check: it runs
// when an operator opens the route form, must not hold the drawer open, and
// carries a channel credential even though nothing is written. Both bounds are
// therefore tighter than the data plane's.
const (
	upstreamModelListTimeout = 5 * time.Second
	upstreamModelListLimit   = 1 << 20
)

// handleProviderUpstreamModels serves GET /admin/providers/{id}/upstream-models.
//
// It asks the channel's own OpenAI-compatible /models endpoint which deployments
// it offers, so an operator picks a name instead of typing one. A typo in the
// upstream name is otherwise invisible until a real request fails, and it is the
// most error-prone field in the route form.
//
// Only names are returned. The upstream body is never echoed: it can contain
// channel-specific detail the console has no use for, and the error path must
// not become a way to read an arbitrary URL's response through the gateway.
func (s *Server) handleProviderUpstreamModels(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/admin/providers/"), "/upstream-models")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	providers, err := s.pg.ListProviders(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load the channel", reqID)
		return
	}
	var provider *model.Provider
	for i := range providers {
		if providers[i].ID == id {
			provider = &providers[i]
			break
		}
	}
	if provider == nil {
		httpx.Error(w, http.StatusNotFound, "provider_not_found", "Channel was not found", reqID)
		return
	}

	credential := strings.TrimSpace(os.Getenv(provider.CredentialEnv))
	if credential == "" {
		// The env var name is already part of the channel API, so naming it here
		// tells the operator exactly what to set without revealing any value.
		httpx.Error(w, http.StatusBadRequest, "credential_not_configured",
			fmt.Sprintf("The channel credential %s is not set in the gateway environment", provider.CredentialEnv), reqID)
		return
	}

	target, err := directTargetURL(provider.BaseURL, &url.URL{Path: "/v1/models"})
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_provider", "The channel base URL is invalid", reqID)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), upstreamModelListTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_provider", "The channel base URL is invalid", reqID)
		return
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")

	client := &http.Client{Transport: s.transport.get(upstreamModelListTimeout)}
	resp, err := client.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable",
			"The channel could not be reached or did not answer in time", reqID)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded amount so the connection can be reused, but never keep
		// the body: it is upstream-controlled and may quote the credential.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		httpx.Error(w, http.StatusBadGateway, "upstream_error",
			fmt.Sprintf("The channel answered HTTP %d", resp.StatusCode), reqID)
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, upstreamModelListLimit+1))
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "upstream_error", "The channel response could not be read", reqID)
		return
	}
	if len(body) > upstreamModelListLimit {
		httpx.Error(w, http.StatusBadGateway, "upstream_error", "The channel response is too large", reqID)
		return
	}

	ids, err := parseUpstreamModelIDs(body)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "upstream_error",
			"The channel did not return an OpenAI-compatible model list", reqID)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": ids, "provider": provider.Name})
}

// parseUpstreamModelIDs accepts both shapes an OpenAI-compatible provider may
// return: {"data":[{"id":"..."}]} and a bare array. Unknown entries are skipped
// rather than failing the whole list, and duplicates are collapsed because some
// gateways list the same deployment once per region.
func parseUpstreamModelIDs(body []byte) ([]string, error) {
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	raw := []json.RawMessage(nil)
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Data != nil {
		raw = envelope.Data
	} else if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		var entry struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &entry); err != nil {
			continue
		}
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
