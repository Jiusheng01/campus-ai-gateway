package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
		// Priority decides the band order; within a band the declaration order is
		// kept.
		//
		// Weight is deliberately NOT a sort key here any more. It is the input to
		// the smooth weighted round-robin that chooses which line of a band
		// starts a request (orderRoutes). Sorting by weight would make the
		// fallback chain a fixed strongest-first list and leave weight meaning
		// "tiebreak" rather than "share of traffic".
		sort.SliceStable(routes, func(i, j int) bool {
			return routes[i].Priority < routes[j].Priority
		})
		out.models[m.Alias] = routes
	}
	return out
}

// normalWeight treats a missing or non-positive weight as 1.
//
// Zero is not a meaningful weight — a line with weight 0 would never be picked
// and is indistinguishable from a misconfigured one — so it becomes the neutral
// default rather than a black hole. This matches the route table's CHECK
// (weight >= 0): zero is allowed to exist, it just does not mean "never".
func normalWeight(w int) int {
	if w <= 0 {
		return 1
	}
	return w
}

// routeSelector holds the smooth-weighted-round-robin state that picks which
// line of a priority band starts a request.
//
// Smooth WRR (the nginx algorithm) spreads a band's picks so the weights are met
// over a window rather than in bursts: each member's running credit grows by its
// weight per pick, the highest credit wins, and the winner's credit drops by the
// total. For weights 3:1 the start sequence is A, A, B, A — three to one without
// ever issuing two Bs back to back.
//
// The state is per instance and in memory: it is scheduling state, not durable
// truth, and losing it on restart only resets the rotation. It is keyed by model
// and priority band so two models do not share a rotation.
type routeSelector struct {
	mu      sync.Mutex
	current map[string][]int
}

func newRouteSelector() *routeSelector {
	return &routeSelector{current: make(map[string][]int)}
}

// start returns the index of the band member that should start this request,
// advancing the rotation. The caller must pass a band whose members are in a
// stable order (assembleRouteTable preserves the declaration order).
func (s *routeSelector) start(key string, weights []int) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := s.current[key]
	if len(cur) != len(weights) {
		cur = make([]int, len(weights))
	}
	total := 0
	for i, w := range weights {
		cur[i] += w
		total += w
	}
	pick := 0
	for i := 1; i < len(cur); i++ {
		if cur[i] > cur[pick] {
			pick = i
		}
	}
	cur[pick] -= total
	s.current[key] = cur
	return pick
}

// orderByWeight returns a band in descending weight, keeping declaration order
// for equal weights. It is the order used for the fallback lines of a band and
// for every band after the first.
func orderByWeight(band []directRoute) []directRoute {
	ordered := append([]directRoute(nil), band...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return normalWeight(ordered[i].Weight) > normalWeight(ordered[j].Weight)
	})
	return ordered
}

// orderRoutes returns the attempt order for a model's lines: each priority band
// in turn. The first band's starting line is chosen by smooth weighted round
// robin; the rest of that band, and every later band, are ordered by descending
// weight. Priority still dominates — a healthy lower-priority band is always
// tried before a higher one — the weights only decide who leads within a band.
func (s *Server) orderRoutes(model string, routes []directRoute) []directRoute {
	if len(routes) == 0 {
		return routes
	}

	// Split into contiguous priority bands (assembleRouteTable sorted by
	// priority, so equal priorities are adjacent).
	var bands [][]directRoute
	for _, r := range routes {
		if len(bands) == 0 || bands[len(bands)-1][0].Priority != r.Priority {
			bands = append(bands, []directRoute{r})
			continue
		}
		bands[len(bands)-1] = append(bands[len(bands)-1], r)
	}

	out := make([]directRoute, 0, len(routes))
	for i, band := range bands {
		if i == 0 && s.selector != nil && len(band) > 1 {
			weights := make([]int, len(band))
			for j, r := range band {
				weights[j] = normalWeight(r.Weight)
			}
			key := model + "\x00" + strconv.Itoa(band[0].Priority)
			pick := s.selector.start(key, weights)
			out = append(out, band[pick])
			rest := make([]directRoute, 0, len(band)-1)
			rest = append(rest, band[:pick]...)
			rest = append(rest, band[pick+1:]...)
			out = append(out, orderByWeight(rest)...)
			continue
		}
		out = append(out, orderByWeight(band)...)
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
	configured := table.models[envelope.Model]
	if len(configured) == 0 {
		httpx.Error(w, http.StatusNotFound, "model_not_configured",
			"No enabled channel is configured for model "+envelope.Model, reqID)
		return
	}
	routes := s.orderRoutes(envelope.Model, configured)

	var (
		lastStatus  int
		lastBody    []byte
		lastErr     error
		lastChannel string // the channel that produced whichever outcome is reported
		attempts    int
	)
	recorder := attemptRecorderFrom(r.Context())
	for _, route := range routes {
		attempts++
		started := time.Now()
		resp, err := s.attemptDirect(r, body, envelope, route)
		if err != nil {
			lastErr = err
			lastStatus = 0
			lastBody = nil
			lastChannel = route.Provider
			recordAttempt(recorder, model.RequestAttempt{
				AttemptNo: attempts, Provider: route.Provider, UpstreamModel: route.UpstreamModel,
				Outcome: model.AttemptFailed, LatencyMS: time.Since(started).Milliseconds(), ErrorKind: attemptErrorKind(err),
			})
			continue
		}
		if shouldFailover(resp.StatusCode) {
			// Drain a little of the body so the connection can be reused, and
			// keep it for the server-side log. It is never copied to the client:
			// an upstream body is not a trusted place to quote operator-facing
			// text from.
			lastBody, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			lastStatus = resp.StatusCode
			lastErr = nil
			lastChannel = route.Provider
			recordAttempt(recorder, model.RequestAttempt{
				AttemptNo: attempts, Provider: route.Provider, UpstreamModel: route.UpstreamModel,
				Outcome: model.AttemptFailed, StatusCode: resp.StatusCode, LatencyMS: time.Since(started).Milliseconds(),
			})
			continue
		}
		recordAttempt(recorder, model.RequestAttempt{
			AttemptNo: attempts, Provider: route.Provider, UpstreamModel: route.UpstreamModel,
			Outcome: model.AttemptServed, StatusCode: resp.StatusCode, LatencyMS: time.Since(started).Milliseconds(),
		})
		writeDirectResponse(w, resp, route, attempts)
		return
	}

	// Every line failed. Record how many were tried before answering: the
	// request log would otherwise show a total failure with no attempt count,
	// which reads as "nothing was attempted" rather than "everything was".
	w.Header().Set("X-ThinkWatch-Attempts", strconv.Itoa(attempts))

	// The client gets a stable code and the attempt count; the server log gets
	// the gateway's own facts about the failure. Neither carries upstream
	// content.
	//
	// R4: the log used to quote the upstream error body and the raw transport
	// error. Both are places a channel credential reaches. An upstream that
	// reflects the Authorization header it was sent puts the gateway's channel
	// key in its error body, and a url.Error quotes the dialled target, so a
	// malformed base_url carrying userinfo has its password printed verbatim.
	// The log is retained, shipped and read by people who are not entitled to
	// the channel secret, so nothing upstream-derived is written to it. What
	// stays is the gateway's own observation - request id, model, status,
	// channel, attempt count and body size - plus a fixed phrase chosen from
	// the error's type.
	switch {
	case lastStatus != 0:
		// Every line answered, none usefully. Report the last status rather
		// than inventing a 502: the client can then tell an upstream 429 from a
		// gateway failure.
		//
		// The model name is client-supplied, so it is single-lined before it
		// reaches the log; the request id ties the line to its log row. The
		// body is reported as a size, never quoted.
		log.Printf("direct: request %s model %q failed on every channel after %d attempt(s); last status %d from %s, response body %d bytes",
			reqID, oneLine(envelope.Model), attempts, lastStatus, oneLine(lastChannel), len(lastBody))
		httpx.Error(w, lastStatus, "upstream_error",
			fmt.Sprintf("all %d channel(s) failed; last status %d", attempts, lastStatus), reqID)
	case lastErr != nil:
		log.Printf("direct: request %s model %q could not reach channel %s: %s",
			reqID, oneLine(envelope.Model), oneLine(lastChannel), directFailureReason(lastErr))
		if detail := clientSafeFailure(lastErr); detail != "" {
			httpx.Error(w, http.StatusBadGateway, "upstream_unavailable",
				"no channel could serve the request: "+detail, reqID)
			return
		}
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable",
			"no channel could serve the request", reqID)
	default:
		httpx.Error(w, http.StatusBadGateway, "upstream_unavailable", "no channel could serve the request", reqID)
	}
}

// recordAttempt appends one Routing Attempt to the request's recorder, if the
// data plane was reached through the logging wrapper. A nil recorder is the
// normal case for a unit test or a probe and means "nothing to record", not an
// error.
func recordAttempt(a *attemptRecorder, attempt model.RequestAttempt) {
	if a == nil {
		return
	}
	a.record(attempt)
}

// attemptErrorKind classifies an attempt failure for telemetry. It returns the
// gateway's own label, never the error text: a transport error can quote the
// dialled URL, userinfo included (see directFailureReason).
func attemptErrorKind(err error) string {
	var missing missingCredentialError
	if errors.As(err, &missing) {
		return "missing_credential"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	return "transport"
}

// clientSafeFailure returns the part of an attempt error that is safe to show
// the caller, or "" when nothing is.
//
// A missing credential is a configuration mistake the operator can fix, and it
// names an environment variable rather than its value. Transport and upstream
// errors can carry internal addresses and upstream credentials, so they stay
// in the log.
func clientSafeFailure(err error) string {
	var missing missingCredentialError
	if errors.As(err, &missing) {
		return missing.Error()
	}
	return ""
}

// missingCredentialError says a channel has no credential configured.
//
// It is safe to show the caller: it names an environment variable, never its
// value. Transport and upstream errors are not safe, because they can carry
// internal addresses and upstream credentials, so the caller-facing code
// distinguishes the two by type rather than by matching message text.
type missingCredentialError struct {
	Channel string
	Env     string
}

func (e missingCredentialError) Error() string {
	return fmt.Sprintf("channel %s: %s is empty", e.Channel, e.Env)
}

// directFailureReason classifies a failed attempt for the server log.
//
// A transport error's text is not safe to write. net/http wraps a dial failure
// in a url.Error that quotes the dialled URL, and a base_url the gateway cannot
// parse is quoted verbatim - userinfo and password included - in the error the
// direct plane builds from it. This returns a fixed phrase selected from the
// error's type, never its message, so no credential that reached the error can
// reach the log.
//
// A missing credential is the one case that keeps real text, and it is shared
// with the caller-facing summary on purpose: it names an environment variable,
// never its value, and it is the single failure an operator can fix from the
// log line alone. Reusing clientSafeFailure keeps the two audiences from
// drifting apart.
func directFailureReason(err error) string {
	if detail := clientSafeFailure(err); detail != "" {
		return detail
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "the channel host could not be resolved"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "the channel did not answer in time"
	}
	return "the channel could not be reached"
}

// attemptDirect sends one request to one channel.
func (s *Server) attemptDirect(r *http.Request, body []byte, envelope requestEnvelope, route directRoute) (*http.Response, error) {
	credential := strings.TrimSpace(os.Getenv(route.CredentialEnv))
	if credential == "" {
		return nil, missingCredentialError{Channel: route.Provider, Env: route.CredentialEnv}
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

// forwardableResponseHeaders is the allow list ADR-0012 names for response
// headers: content type, caching, retry and standard rate-limit metadata. W3C
// trace context is included so a client can correlate the request with the
// upstream's trace, and the internal request id so the console can link a
// failed response to its log row.
//
// Content-Length is copied because the body is streamed unchanged; leaving it
// off would switch every non-streaming response to chunked encoding.
var forwardableResponseHeaders = map[string]bool{
	"Content-Type":         true,
	"Content-Length":       true,
	"Content-Encoding":     true,
	"Cache-Control":        true,
	"ETag":                 true,
	"Last-Modified":        true,
	"Expires":              true,
	"Vary":                 true,
	"Retry-After":          true,
	"RateLimit-Limit":      true,
	"RateLimit-Remaining":  true,
	"RateLimit-Reset":      true,
	"X-RateLimit-Limit":    true,
	"X-RateLimit-Remaining": true,
	"X-RateLimit-Reset":    true,
	"Traceparent":          true,
	"Tracestate":           true,
	"Request-Id":           true,
	"X-Request-Id":         true,
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

	// Copying headers is a security boundary, not a formality. The gateway
	// injects the channel credential on the way out, so an upstream that
	// reflects its request headers back (some proxies and debugging gateways
	// do) would hand the caller the gateway's own credential. Cookies and
	// challenges are likewise the upstream's business, not the tenant's.
	//
	// ADR-0009 and ADR-0012 require an explicit allow list here rather than a
	// deny list: content type, caching, retry and rate-limit metadata, plus
	// W3C trace context. A deny list has to anticipate every credential-bearing
	// header an upstream might invent; an allow list only has to name the ones
	// a client can legitimately use. Redirects, server identity, upstream CORS
	// headers and connection-scoped headers are therefore excluded by default.
	for k, values := range resp.Header {
		if !forwardableResponseHeaders[http.CanonicalHeaderKey(k)] {
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
	// The failure summary names the channel and the status; it never carries
	// the upstream body or the dial error, which is where internal detail
	// lives. The operator needs to know which line failed, not what the
	// upstream wrote to its own log.
	var failures []string
	for _, route := range routes {
		resp, err := s.attemptDirect(probe, payload, envelope, route)
		if err != nil {
			if detail := clientSafeFailure(err); detail != "" {
				failures = append(failures, detail)
			} else {
				failures = append(failures, fmt.Sprintf("%s: unreachable", route.Provider))
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("%s: response could not be read", route.Provider))
			continue
		}
		if shouldFailover(resp.StatusCode) {
			failures = append(failures, fmt.Sprintf("%s: HTTP %d", route.Provider, resp.StatusCode))
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
	httpx.Error(w, http.StatusBadGateway, "upstream_unavailable", message, reqID)
}

// oneLine collapses a client-supplied value to a single line for logging, so a
// model name containing newlines cannot forge a log entry.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
