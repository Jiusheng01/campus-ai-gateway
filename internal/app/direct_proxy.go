package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The direct data plane.
//
// The gateway has always been a control plane: it authenticates, meters and
// logs, then hands the request to ThinkWatch Core, which owns model resolution,
// failover and upstream credentials. That is the right shape when a deployment
// has several channels behind one model, but it also means a campus that has
// exactly one API key still has to run a second process to make a single call.
//
// This file is the other data plane: the same contract, implemented in-process
// for deployments that do not configure THINKWATCH_URL. It resolves the model
// against gateway_model_route, injects the channel's credential from its
// environment variable, streams the response back, and fails over to the next
// line the same way ThinkWatch would.
//
// What it deliberately does not do: weighted routing and circuit breaking.
// Those need per-channel state that outlives a request, and pretending to have
// them would be worse than saying so.

// directRoute is one usable line: a channel plus the upstream model name that
// channel expects.
type directRoute struct {
	Provider      string
	BaseURL       string
	UpstreamModel string
	CredentialEnv string
	Priority      int
	Weight        int
	TimeoutMS     int
}

// routeTable maps an external model name to its lines, best first.
type routeTable struct {
	models map[string][]directRoute
}

// routeCacheTTL is how long a built table is reused.
//
// Rebuilding it per request would put three queries on the hot path. Rebuilding
// it on every admin write would mean touching eight handlers and getting one of
// them wrong — and a missed one is invisible until routing goes stale. A short
// TTL makes a saved change visible within seconds, which is well inside what an
// operator expects after pressing save.
const routeCacheTTL = 5 * time.Second

// defaultUpstreamTimeout bounds how long an upstream may take to send response
// headers. It is a header deadline, not a body deadline: a streamed completion
// may legitimately run for minutes.
const defaultUpstreamTimeout = 60 * time.Second

// transportPool keeps one transport per distinct header timeout.
//
// ResponseHeaderTimeout lives on the transport, not on the request, so honouring
// a per-route timeout_ms means a transport per timeout value. There are only a
// handful of distinct values in practice, and sharing them keeps connection
// reuse working.
type transportPool struct {
	mu sync.Mutex
	m  map[time.Duration]*http.Transport
}

func (p *transportPool) get(timeout time.Duration) *http.Transport {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.m[timeout]; ok {
		return t
	}
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
	}
	if p.m == nil {
		p.m = make(map[time.Duration]*http.Transport)
	}
	p.m[timeout] = t
	return t
}

// routeTableCached returns the current routing table, rebuilding it at most
// once per routeCacheTTL.
func (s *Server) routeTableCached(ctx context.Context) (routeTable, error) {
	s.routeMu.Lock()
	if s.routeCache.models != nil && time.Since(s.routeAt) < routeCacheTTL {
		cached := s.routeCache
		s.routeMu.Unlock()
		return cached, nil
	}
	s.routeMu.Unlock()

	built, err := s.buildRouteTable(ctx)
	if err != nil {
		// Keep serving the previous table: a transient database error must not
		// take business traffic down with it.
		s.routeMu.Lock()
		previous := s.routeCache
		s.routeMu.Unlock()
		if previous.models != nil {
			return previous, nil
		}
		return routeTable{}, err
	}

	s.routeMu.Lock()
	s.routeCache = built
	s.routeAt = time.Now()
	s.routeMu.Unlock()
	return built, nil
}

// buildRouteTable reads channels, models and lines, then hands them to
// assembleRouteTable. Keeping the reads and the decisions apart is what makes
// the decisions testable without a database.
func (s *Server) buildRouteTable(ctx context.Context) (routeTable, error) {
	providers, err := s.pg.ListProviders(ctx)
	if err != nil {
		return routeTable{}, err
	}
	models, err := s.pg.ListModels(ctx)
	if err != nil {
		return routeTable{}, err
	}
	routesByModel := make(map[string][]model.ModelRoute, len(models))
	for _, m := range models {
		lines, err := s.pg.ListModelRoutes(ctx, m.ID)
		if err != nil {
			return routeTable{}, err
		}
		routesByModel[m.ID] = lines
	}
	return assembleRouteTable(providers, models, routesByModel), nil
}

// assembleRouteTable keeps only the entries that could actually serve a
// request, and orders each model's lines best first.
//
// "Enabled" is status == 1 throughout. Disabled is 2, not 0 — the write paths
// normalise a zero to 1 — so testing for zero would quietly route traffic to
// every channel an operator had switched off.
func assembleRouteTable(providers []model.Provider, models []model.ModelConfig, routesByModel map[string][]model.ModelRoute) routeTable {
	enabled := make(map[string]model.Provider, len(providers))
	for _, p := range providers {
		if p.Status != 1 || strings.TrimSpace(p.BaseURL) == "" {
			continue
		}
		enabled[p.ID] = p
	}

	out := routeTable{models: make(map[string][]directRoute, len(models))}
	for _, m := range models {
		if m.Status != 1 {
			continue
		}
		routes := make([]directRoute, 0, len(routesByModel[m.ID]))
		for _, rt := range routesByModel[m.ID] {
			if rt.Status != 1 {
				continue
			}
			p, ok := enabled[rt.ProviderID]
			if !ok {
				continue
			}
			upstream := strings.TrimSpace(rt.UpstreamModel)
			if upstream == "" {
				upstream = strings.TrimSpace(m.UpstreamModel)
			}
			routes = append(routes, directRoute{
				Provider:      p.Name,
				BaseURL:       p.BaseURL,
				UpstreamModel: upstream,
				CredentialEnv: p.CredentialEnv,
				Priority:      rt.Priority,
				Weight:        rt.Weight,
				TimeoutMS:     rt.TimeoutMS,
			})
		}
		if len(routes) == 0 {
			continue
		}
		// Primary first, then the heavier line of a priority band, then the
		// order they were declared in.
		//
		// The weight tiebreak is stated here rather than inherited from the
		// query's ORDER BY. Relying on the caller's order made the function's
		// contract depend on a SQL clause three files away — and weight would
		// have been silently dropped by any future caller that did not
		// pre-sort. Weight still does not mean weighted routing here; it only
		// orders the fallback chain.
		sort.SliceStable(routes, func(i, j int) bool {
			if routes[i].Priority != routes[j].Priority {
				return routes[i].Priority < routes[j].Priority
			}
			return routes[i].Weight > routes[j].Weight
		})
		out.models[m.Alias] = routes
	}
	return out
}

// serveDirect answers a business request without ThinkWatch.
func (s *Server) serveDirect(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")

	// handleV1 already read and re-wound the body under the same cap; the limit
	// is repeated here so this handler is safe on its own terms rather than
	// only in the one place it happens to be mounted.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBody(r.URL.Path)))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Unable to read request body", reqID)
		return
	}
	_ = r.Body.Close()

	envelope, err := parseRequestEnvelope(body, r.Header.Get("Content-Type"))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body could not be parsed", reqID)
		return
	}
	if envelope.Model == "" {
		httpx.Error(w, http.StatusBadRequest, "model_required", "Model is required", reqID)
		return
	}

	table, err := s.routeTableCached(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load routing table", reqID)
		return
	}
	routes := table.models[envelope.Model]
	if len(routes) == 0 {
		httpx.Error(w, http.StatusNotFound, "model_not_configured",
			"No enabled channel is configured for model "+envelope.Model, reqID)
		return
	}

	var (
		lastStatus int
		lastBody   []byte
		lastErr    error
		attempts   int
	)
	for _, route := range routes {
		attempts++
		resp, err := s.attemptDirect(r, body, envelope, route)
		if err != nil {
			lastErr = err
			continue
		}
		if shouldFailover(resp.StatusCode) {
			// Drain a little of the body so the connection can be reused, and
			// keep it in case this was the last line.
			lastBody, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			lastStatus = resp.StatusCode
			continue
		}
		writeDirectResponse(w, resp, route, attempts)
		return
	}

	// Every line failed. Record how many were tried before answering: the
	// request log would otherwise show a total failure with no attempt count,
	// which reads as "nothing was attempted" rather than "everything was".
	w.Header().Set("X-ThinkWatch-Attempts", strconv.Itoa(attempts))

	switch {
	case lastStatus != 0:
		// Every line answered, none usefully. Report the last status rather
		// than inventing a 502: the client can then tell an upstream 429 from a
		// gateway failure.
		httpx.Error(w, lastStatus, "upstream_error",
			fmt.Sprintf("all %d channel(s) failed; last status %d: %s", attempts, lastStatus, excerpt(lastBody, 500)), reqID)
	case lastErr != nil:
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable",
			"no channel could serve the request: "+lastErr.Error(), reqID)
	default:
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable", "no channel could serve the request", reqID)
	}
}

// attemptDirect sends one request to one channel.
func (s *Server) attemptDirect(r *http.Request, body []byte, envelope requestEnvelope, route directRoute) (*http.Response, error) {
	credential := strings.TrimSpace(os.Getenv(route.CredentialEnv))
	if credential == "" {
		return nil, fmt.Errorf("channel %s: %s is empty", route.Provider, route.CredentialEnv)
	}

	target, err := directTargetURL(route.BaseURL, r.URL)
	if err != nil {
		return nil, fmt.Errorf("channel %s: %w", route.Provider, err)
	}

	payload := body
	if route.UpstreamModel != "" && route.UpstreamModel != envelope.Model {
		// The external model name is the gateway's; the channel expects its
		// own. Multipart bodies are left alone: rewriting a form field would
		// mean rebuilding the body, and the alias would have to differ from
		// the channel's model name for that to matter.
		if rewritten, ok := rewriteJSONModel(body, route.UpstreamModel); ok {
			payload = rewritten
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("channel %s: %w", route.Provider, err)
	}
	for k, values := range r.Header {
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "Host", "Content-Length", "Connection", "Keep-Alive",
			"Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
			continue
		}
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept-Encoding", "identity")

	timeout := time.Duration(route.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultUpstreamTimeout
	}
	client := &http.Client{Transport: s.transport.get(timeout)}
	return client.Do(req)
}

// writeDirectResponse copies the upstream response to the client and stamps the
// telemetry headers the request logger reads.
//
// The header names still say ThinkWatch. They are an internal contract between
// the data plane and the logging wrapper, and renaming them would churn every
// test that pins attribution; what matters is that they carry the truth in both
// modes.
func writeDirectResponse(w http.ResponseWriter, resp *http.Response, route directRoute, attempts int) {
	defer resp.Body.Close()

	for k, values := range resp.Header {
		switch http.CanonicalHeaderKey(k) {
		case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
			"Te", "Trailer", "Transfer-Encoding", "Upgrade":
			continue
		}
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-ThinkWatch-Provider", route.Provider)
	w.Header().Set("X-ThinkWatch-Upstream-Model", route.UpstreamModel)
	w.Header().Set("X-ThinkWatch-Route-Priority", strconv.Itoa(route.Priority))
	w.Header().Set("X-ThinkWatch-Attempts", strconv.Itoa(attempts))

	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// serveDirectModels answers GET /v1/models from the routing table.
//
// In ThinkWatch mode this call is proxied. In direct mode there is nothing to
// proxy to, and the honest answer is the set of models that can actually be
// routed right now.
func (s *Server) serveDirectModels(w http.ResponseWriter, r *http.Request) {
	table, err := s.routeTableCached(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load routing table", r.Header.Get("X-Request-Id"))
		return
	}
	names := make([]string, 0, len(table.models))
	for alias := range table.models {
		names = append(names, alias)
	}
	sort.Strings(names)

	type modelEntry struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	out := make([]modelEntry, 0, len(names))
	for _, name := range names {
		out = append(out, modelEntry{ID: name, Object: "model", OwnedBy: "campus-ai-gateway"})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": out})
}

// directTargetURL joins a channel's base URL with the request path.
//
// A base that already ends in /v1 is the OpenAI-compatible convention, and the
// incoming path repeats it; keeping both would produce /v1/v1/chat/completions.
// A base without /v1 is a vendor root, where the path is already correct.
func directTargetURL(base string, u *url.URL) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", fmt.Errorf("base_url is empty")
	}
	suffix := u.Path
	if strings.HasSuffix(base, "/v1") {
		suffix = strings.TrimPrefix(u.Path, "/v1")
	}
	if suffix == "" {
		suffix = "/"
	}
	target := base + suffix
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	if _, err := url.Parse(target); err != nil {
		return "", fmt.Errorf("base_url %q produced an invalid URL: %w", base, err)
	}
	return target, nil
}

// rewriteJSONModel replaces the model field of a JSON body, leaving every other
// byte of meaning intact. It reports false for a body it cannot rewrite, so the
// caller can fall back to sending the original.
func rewriteJSONModel(body []byte, upstream string) ([]byte, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, false
	}
	if _, ok := obj["model"]; !ok {
		return nil, false
	}
	encoded, err := json.Marshal(upstream)
	if err != nil {
		return nil, false
	}
	obj["model"] = encoded
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false
	}
	return out, true
}

// shouldFailover reports whether another line has a chance of doing better.
//
// A 400 means the request is wrong and every channel will say so, so failing
// over would only multiply the latency. A rejected credential, a missing model,
// a rate limit or a server error are all channel-specific, which is exactly
// what a second line is for.
func shouldFailover(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return status >= 500
}

// serveModelTestDirect runs the admin connectivity test without ThinkWatch.
//
// It walks the same route list and applies the same failover policy as
// serveDirect, so a test that passes here is evidence about the path a real
// request would take. The one difference is intent: a probe wants to know which
// channel answered, so the winner is reported back rather than discarded.
func (s *Server) serveModelTestDirect(w http.ResponseWriter, r *http.Request, in thinkWatchTestRequest, payload []byte, reqID string) {
	table, err := s.routeTableCached(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "database_error", "Unable to load routing table", reqID)
		return
	}
	routes := table.models[in.Model]
	if len(routes) == 0 {
		httpx.Error(w, http.StatusNotFound, "model_not_configured",
			"No enabled channel is configured for model "+in.Model, reqID)
		return
	}

	// attemptDirect reads only the method, URL, headers and context off the
	// request, so a relative URL is a complete stand-in for the real one.
	probe, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/v1/chat/completions", nil)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "test_request_failed", "Unable to create the test request", reqID)
		return
	}
	probe.Header.Set("Content-Type", "application/json")

	envelope := requestEnvelope{Model: in.Model}
	started := time.Now()
	var failures []string
	for _, route := range routes {
		resp, err := s.attemptDirect(probe, payload, envelope, route)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", route.Provider, readErr))
			continue
		}
		if shouldFailover(resp.StatusCode) {
			failures = append(failures, fmt.Sprintf("%s: HTTP %d %s", route.Provider, resp.StatusCode, excerpt(body, 200)))
			continue
		}
		result, err := parseProbeResponse(body, time.Since(started))
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: response is not valid JSON", route.Provider))
			continue
		}
		result.Channel = route.Provider
		writeJSON(w, http.StatusOK, result)
		return
	}

	message := "no channel could serve the test request"
	if len(failures) > 0 {
		message += ": " + strings.Join(failures, "; ")
	}
	httpx.Error(w, http.StatusBadGateway, "upstream_unavailable", excerpt([]byte(message), 900), reqID)
}

// excerpt renders an upstream body for an error message: single line, bounded,
// and cut on a rune boundary.
//
// The rune boundary is not pedantry. Upstream error bodies are routinely
// Chinese, and slicing the byte string at an arbitrary offset produces invalid
// UTF-8 that the console then renders as a replacement character — which reads
// like a gateway bug rather than the upstream's message.
func excerpt(body []byte, limit int) string {
	text := strings.TrimSpace(strings.Join(strings.Fields(string(body)), " "))
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}
