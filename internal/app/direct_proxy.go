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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// The data plane.
//
// The gateway is a control plane: it authenticates, meters and logs, then
// resolves the model and dials the channel that serves it.
//
// This file is that data plane, implemented in-process. It resolves the model
// against gateway_model_route, injects the channel's credential from its
// environment variable, streams the response back, and fails over to the next
// line.
//
// It carries the per-channel state that outlives a request: a smooth
// weighted round robin picks which line of a priority band starts (orderRoutes)
// and a per-provider circuit breaker skips a channel that keeps failing
// (breaker.go). Both are per instance and in memory.

// directRoute is one usable line: a channel plus the upstream model name that
// channel expects.
//
// The Credential fields describe where the channel secret comes from. An empty
// CredentialSource means env, which keeps the existing route fixtures — and any
// path that still carries only CredentialEnv — working.
type directRoute struct {
	Provider      string
	BaseURL       string
	UpstreamModel string
	// CredentialEnv is the environment-variable name for the legacy env source.
	CredentialEnv string
	// CredentialSource is "env" or "inline".
	CredentialSource string
	// CredentialRef is the environment-variable name for an env credential.
	CredentialRef string
	// CredentialCiphertext is the sealed value for an inline credential.
	CredentialCiphertext string
	Priority             int
	Weight               int
	TimeoutMS            int
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
	m  map[transportKey]*http.Transport
}

// transportKey keeps one transport per timeout and policy. The policy is fixed
// for a Server's lifetime, but including it makes the pool honest about what a
// cached transport dials with.
type transportKey struct {
	timeout time.Duration
	policy  egressPolicy
}

func (p *transportPool) get(timeout time.Duration, policy egressPolicy) *http.Transport {
	key := transportKey{timeout: timeout, policy: policy}
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.m[key]; ok {
		return t
	}
	// ADR-0012: every connection is checked against the address the resolver
	// produced for it. Control runs at exactly that point, which is what makes
	// the DNS answer revalidated per connection rather than trusted from a
	// lookup made earlier.
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   policy.control,
	}
	t := &http.Transport{
		// ADR-0012: Provider egress dials directly. ProxyFromEnvironment would
		// make Dialer.Control see the proxy's address instead of the Provider's,
		// so the address denial and the per-connection DNS revalidation would
		// both be bypassed whenever HTTP(S)_PROXY is set. There is deliberately
		// no configuration knob: a proxy setting must not route Provider traffic
		// around the policy.
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: time.Second,
	}
	if p.m == nil {
		p.m = make(map[transportKey]*http.Transport)
	}
	p.m[key] = t
	return t
}

// routeTableCached returns the current routing table, rebuilding it at most
// once per routeCacheTTL.
//
// Invalidation advances a generation. A rebuild captures the generation before
// it reads, and publishes its result only if the generation is unchanged. A
// rebuild that overlapped a write is therefore discarded instead of
// overwriting the new configuration with an older snapshot.
func (s *Server) routeTableCached(ctx context.Context) (routeTable, error) {
	var built routeTable
	for attempt := 0; attempt < 3; attempt++ {
		s.routeMu.Lock()
		if s.routeCache.models != nil && time.Since(s.routeAt) < routeCacheTTL {
			cached := s.routeCache
			s.routeMu.Unlock()
			return cached, nil
		}
		generation := s.routeGen
		builder := s.routeBuildFn
		s.routeMu.Unlock()

		var err error
		if builder != nil {
			built, err = builder(ctx)
		} else {
			built, err = s.buildRouteTable(ctx)
		}
		if err != nil {
			// Keep serving the previous table: a transient database error must not
			// take business traffic down with it.
			s.routeMu.Lock()
			s.routeBuildErr = "route_snapshot_failed"
			previous := s.routeCache
			s.routeMu.Unlock()
			if previous.models != nil {
				return previous, nil
			}
			return routeTable{}, err
		}

		s.routeMu.Lock()
		if s.routeGen != generation {
			s.routeMu.Unlock()
			continue
		}
		s.routeCache = built
		s.routeAt = time.Now()
		s.routeCacheGen = generation
		s.routeBuildErr = ""
		s.routeMu.Unlock()
		return built, nil
	}
	// Continuous writes kept invalidating the build. Return the last complete
	// snapshot without caching it; the next request will try again.
	return built, nil
}

// invalidateRouteCache makes the next request rebuild from the database. It is
// called after a successful routing-affecting management write.
func (s *Server) invalidateRouteCache() {
	s.routeMu.Lock()
	s.routeGen++
	s.routeAt = time.Time{}
	s.routeMu.Unlock()
}

type routeCacheStatus struct {
	BuiltAt       *time.Time `json:"built_at"`
	AgeMS         int64      `json:"age_ms"`
	Stale         bool       `json:"stale"`
	LastErrorKind string     `json:"last_error_kind,omitempty"`
	Generation    uint64     `json:"generation"`
}

func (s *Server) routeCacheState() routeCacheStatus {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	out := routeCacheStatus{LastErrorKind: s.routeBuildErr, Generation: s.routeGen, Stale: s.routeBuildErr != "" && s.routeCache.models != nil}
	if !s.routeAt.IsZero() {
		at := s.routeAt.UTC()
		out.BuiltAt = &at
		out.AgeMS = time.Since(s.routeAt).Milliseconds()
	}
	return out
}

// buildRouteTable reads one consistent snapshot, then hands it to
// assembleRouteTable. Keeping the read and the decisions apart is what makes
// the decisions testable without a database.
func (s *Server) buildRouteTable(ctx context.Context) (routeTable, error) {
	snapshot, err := s.pg.RouteConfigSnapshot(ctx)
	if err != nil {
		return routeTable{}, err
	}
	return assembleRouteTable(snapshot.Providers, snapshot.Models, snapshot.Routes), nil
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
			// A Route without an upstream model is not a usable line. The
			// retired model column is not a fallback: filling it from there
			// would make a deleted field a second routing authority again.
			upstream := strings.TrimSpace(rt.UpstreamModel)
			if upstream == "" {
				continue
			}
			source := p.CredentialSource
			ref := p.CredentialSecretRef
			if source == "" {
				// A provider fixture or a pre-Credential row: name the variable
				// directly. The data plane treats an empty source as env.
				source = model.CredentialSourceEnv
				ref = p.CredentialEnv
			}
			routes = append(routes, directRoute{
				Provider:             p.Name,
				BaseURL:              p.BaseURL,
				UpstreamModel:        upstream,
				CredentialEnv:        ref,
				CredentialSource:     source,
				CredentialRef:        ref,
				CredentialCiphertext: p.CredentialCiphertext,
				Priority:             rt.Priority,
				Weight:               rt.Weight,
				TimeoutMS:            rt.TimeoutMS,
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

// serveDirect answers a business request.
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
		chainNo     int    // position in the attempt chain, skipped lines included
	)
	recorder := attemptRecorderFrom(r.Context())
	tried := 0 // channels actually dialled; a skipped line is NOT an attempt
	for _, route := range routes {
		chainNo++
		// A channel whose circuit is open is skipped, not tried: that is the
		// whole point of the breaker. It still leaves a line in the chain so the
		// console can tell "not tried" from "tried and failed" — but it is not a
		// Routing Attempt, so it must not inflate the attempt count or the
		// failover flag.
		if s.breaker != nil && !s.breaker.allow(route.Provider) {
			recordAttempt(recorder, attemptFor(chainNo, route, model.AttemptSkipped, 0, 0, ""))
			continue
		}
		tried++
		started := time.Now()
		resp, err := s.attemptDirect(r, body, envelope, route)
		if err != nil {
			s.recordBreaker(route.Provider, false)
			s.recordEgressDenial(r, reqID, envelope.Model, err)
			lastErr = err
			lastStatus = 0
			lastBody = nil
			lastChannel = route.Provider
			recordAttempt(recorder, attemptFor(chainNo, route, model.AttemptFailed, 0, time.Since(started).Milliseconds(), attemptErrorKind(err)))
			continue
		}
		if shouldFailover(resp.StatusCode) {
			s.recordBreaker(route.Provider, false)
			// Drain a little of the body so the connection can be reused, and
			// keep it for the server-side log. It is never copied to the client:
			// an upstream body is not a trusted place to quote operator-facing
			// text from.
			lastBody, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			lastStatus = resp.StatusCode
			lastErr = nil
			lastChannel = route.Provider
			recordAttempt(recorder, attemptFor(chainNo, route, model.AttemptFailed, resp.StatusCode, time.Since(started).Milliseconds(), ""))
			continue
		}
		// The channel answered: a 2xx, or a terminal 4xx that is the request's
		// fault rather than the channel's, so it still counts as a healthy
		// answer for the breaker.
		s.recordBreaker(route.Provider, true)
		recordAttempt(recorder, attemptFor(chainNo, route, model.AttemptServed, resp.StatusCode, time.Since(started).Milliseconds(), ""))
		writeDirectResponse(w, resp, route, tried)
		return
	}

	if tried == 0 {
		// Every line was circuit-broken, so nothing was actually attempted. Say
		// so rather than reporting an empty "all channels failed" with no status.
		log.Printf("direct: request %s model %q had every channel circuit-broken", reqID, oneLine(envelope.Model))
		httpx.Error(w, http.StatusServiceUnavailable, "channels_circuit_open",
			"no channel is currently available; every line is circuit-broken", reqID)
		return
	}

	// Every line failed. Record how many were actually tried before answering:
	// the request log would otherwise show a total failure with no attempt count,
	// which reads as "nothing was attempted" rather than "everything was".
	w.Header().Set(headerAttempts, strconv.Itoa(tried))

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
			reqID, oneLine(envelope.Model), tried, lastStatus, oneLine(lastChannel), len(lastBody))
		httpx.Error(w, lastStatus, "upstream_error",
			fmt.Sprintf("all %d channel(s) failed; last status %d", tried, lastStatus), reqID)
	case lastErr != nil:
		// An egress refusal is a policy decision, not an upstream outage. It
		// gets its own code and the channel+reason text so an operator can see
		// what to change instead of reading another generic 502.
		var denied *egressDeniedError
		if errors.As(lastErr, &denied) {
			// The log keeps the full refusal, resolved address included; the
			// caller gets the fixed summary writeEgressDenied builds.
			log.Printf("direct: request %s model %q was denied egress for channel %s: %s",
				reqID, oneLine(envelope.Model), oneLine(lastChannel), denied.Error())
			writeEgressDenied(w, reqID, denied)
			return
		}
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

// attemptFor builds one entry of the attempt chain. no is the position in the
// chain (every line considered, skipped ones included, so the console can show
// the sequence); status, latency and errorKind are the attempt's own, and are
// zero/empty when they do not apply.
func attemptFor(no int, route directRoute, outcome string, status int, latencyMS int64, errorKind string) model.RequestAttempt {
	return model.RequestAttempt{
		AttemptNo:     no,
		Provider:      route.Provider,
		UpstreamModel: route.UpstreamModel,
		Outcome:       outcome,
		StatusCode:    status,
		LatencyMS:     latencyMS,
		ErrorKind:     errorKind,
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

// recordBreaker applies one attempt's verdict to its provider's circuit, if a
// breaker is configured. A nil breaker is the case for a probe or a Server
// built without the direct plane.
func (s *Server) recordBreaker(provider string, ok bool) {
	if s.breaker != nil {
		s.breaker.record(provider, ok)
	}
}

// egressDenialEventType is the security-event label for a denied Provider
// target. The event stream is where an auditor looks for "someone pointed a
// channel at the gateway or its metadata service", which is a different
// question from "which requests failed".
const egressDenialEventType = "provider_egress_denied"

// writeEgressDenied answers a denied Provider target for both the data plane
// and the admin refresh. It is the one place the response is built, so neither
// caller can drift back to echoing the dial target: the code is stable and the
// message is denied.SafeSummary(), which never carries the resolved address
// (ADR-0009). The full refusal stays in the log and the security event.
func writeEgressDenied(w http.ResponseWriter, reqID string, denied *egressDeniedError) {
	httpx.Error(w, http.StatusBadGateway, "provider_egress_denied", denied.SafeSummary(), reqID)
}

// recordEgressDenial writes an egress refusal to the existing security-event
// stream. It is a no-op for anything that is not a denial, and for a Server
// without a database (a unit test or a probe).
func (s *Server) recordEgressDenial(r *http.Request, reqID, requestedModel string, err error) {
	var denied *egressDeniedError
	if !errors.As(err, &denied) {
		return
	}
	event := model.SecurityEvent{
		RequestID:      reqID,
		EventType:      egressDenialEventType,
		ClientIP:       s.clientIPFrom(r),
		RequestedModel: requestedModel,
		Detail:         denied.Error(),
	}
	if id, ok := httpx.IdentityFrom(r.Context()); ok {
		event.TenantID = id.TenantID
		event.APIKeyID = id.APIKeyID
	}
	s.recordSecurityEvent(r, event)
}

// attemptErrorKind classifies an attempt failure for telemetry. It returns the
// gateway's own label, never the error text: a transport error can quote the
// dialled URL, userinfo included (see directFailureReason).
func attemptErrorKind(err error) string {
	var missing missingCredentialError
	if errors.As(err, &missing) {
		return errorKindMissingCredential
	}
	var denied *egressDeniedError
	if errors.As(err, &denied) {
		return errorKindEgressDenied
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return errorKindDNS
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errorKindTimeout
	}
	return errorKindTransport
}

// The gateway's own failure labels for an attempt. They are a closed set, so
// the column and the console agree on what can appear in error_kind.
const (
	errorKindMissingCredential = "missing_credential"
	errorKindEgressDenied      = "egress_denied"
	errorKindDNS               = "dns"
	errorKindTimeout           = "timeout"
	errorKindTransport         = "transport"
)

// The telemetry headers the data plane stamps and the logging wrapper reads.
// They are a contract between the two, so they are spelled once.
const (
	headerProvider      = "X-Gateway-Provider"
	headerUpstreamModel = "X-Gateway-Upstream-Model"
	headerRoutePriority = "X-Gateway-Route-Priority"
	headerAttempts      = "X-Gateway-Attempts"
)

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
	// An egress refusal is the gateway's own policy decision about the target
	// the operator configured. SafeSummary names the channel and the category
	// of address without the resolved literal, which is what makes the failure
	// actionable while keeping network detail out of a response (ADR-0009).
	var denied *egressDeniedError
	if errors.As(err, &denied) {
		return denied.SafeSummary()
	}
	return ""
}

// missingCredentialError says a channel has no usable credential.
//
// It is safe to show the caller: for the env source it names the environment
// variable, never its value; for the inline source it says the stored credential
// is missing or cannot be opened, never anything about the ciphertext. Transport
// and upstream errors are not safe, because they can carry internal addresses
// and upstream credentials, so the caller-facing code distinguishes the two by
// type rather than by matching message text.
//
// Reason, when set, replaces the default "<Env> is empty" message. It exists for
// the inline source, which has no variable name to report, and for a decryption
// failure, which is not the same mistake as a missing one.
type missingCredentialError struct {
	Channel string
	Env     string
	Reason  string
}

func (e missingCredentialError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = e.Env + " is empty"
	}
	return fmt.Sprintf("channel %s: %s", e.Channel, reason)
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
// A missing credential and an egress refusal are the cases that keep real text,
// and both are the failures an operator can fix from the log line alone. A
// denial uses the full Error() here, because the log is the one audience
// allowed to see the resolved address; the caller-facing path uses
// clientSafeFailure, which omits it.
func directFailureReason(err error) string {
	var denied *egressDeniedError
	if errors.As(err, &denied) {
		return denied.Error()
	}
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

// resolveCredential turns a route's Credential into the Bearer value sent
// upstream. It fails closed: an empty variable, a missing inline value and an
// inline value this process cannot open all return missingCredentialError, so no
// request with an empty Authorization is ever dialled.
func (s *Server) resolveCredential(route directRoute) (string, error) {
	source := route.CredentialSource
	if source == "" {
		source = model.CredentialSourceEnv
	}
	switch source {
	case model.CredentialSourceEnv:
		ref := strings.TrimSpace(route.CredentialRef)
		if ref == "" {
			ref = strings.TrimSpace(route.CredentialEnv)
		}
		value := strings.TrimSpace(os.Getenv(ref))
		if value == "" {
			return "", missingCredentialError{Channel: route.Provider, Env: ref}
		}
		return value, nil
	case model.CredentialSourceInline:
		sealed := strings.TrimSpace(route.CredentialCiphertext)
		if sealed == "" {
			return "", missingCredentialError{Channel: route.Provider, Reason: "inline credential is not configured"}
		}
		plaintext, err := security.Open(s.sealingKey(), sealed)
		if err != nil {
			return "", missingCredentialError{Channel: route.Provider, Reason: "inline credential could not be decrypted"}
		}
		value := strings.TrimSpace(string(plaintext))
		if value == "" {
			return "", missingCredentialError{Channel: route.Provider, Reason: "inline credential is empty"}
		}
		return value, nil
	default:
		return "", missingCredentialError{Channel: route.Provider, Reason: "credential source is unknown"}
	}
}

// attemptDirect sends one request to one channel.
func (s *Server) attemptDirect(r *http.Request, body []byte, envelope requestEnvelope, route directRoute) (*http.Response, error) {
	credential, err := s.resolveCredential(route)
	if err != nil {
		return nil, err
	}

	target, err := directTargetURL(route.BaseURL, r.URL)
	if err != nil {
		return nil, fmt.Errorf("channel %s: %w", route.Provider, err)
	}
	// The scheme and the plaintext allowlist are checked here, on the way to
	// the dialer, because Dialer.Control only sees an ip:port. This covers rows
	// that predate the write-time rule as well as hosts whose allowlist entry
	// was removed after the save.
	if reason := s.egressTargetReason(target); reason != "" {
		return nil, &egressDeniedError{Channel: route.Provider, Reason: reason}
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
		if !forwardableRequestHeaders[http.CanonicalHeaderKey(k)] {
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
	// ADR-0012 forbids automatic redirects. Following one would move the
	// channel credential to a host the channel's configuration never named, so
	// the 3xx is surfaced as the answer instead.
	client := &http.Client{
		Transport: s.transport.get(timeout, s.egress),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		// The dial hook knows the address and the rule but not the channel it
		// was dialling for. Attach that here so every audience — the client,
		// the log, the security event — can name the channel and the reason.
		var denied *egressDeniedError
		if errors.As(err, &denied) {
			return nil, &egressDeniedError{Channel: route.Provider, Address: denied.Address, Reason: denied.Reason}
		}
		return nil, err
	}
	return resp, nil
}

// forwardableRequestHeaders is the allow list ADR-0012 names for request
// headers: content negotiation, W3C trace context and the Client Correlation
// ID. It is the mirror of forwardableResponseHeaders and lives beside it so the
// two sides are maintained together.
//
// Everything else stays in the gateway. A tenant's Cookie and its forwarding
// chain (X-Forwarded-For, X-Real-IP) are between the tenant and the gateway,
// not facts a third-party Provider should receive; a deny list would have to
// anticipate every header a browser or proxy might attach, while an allow list
// only names what an upstream can legitimately need. Authorization is not here
// either — it is replaced below with the channel Credential — and Host,
// Content-Length and Connection are transport-managed.
//
// Accept-Encoding is deliberately absent: the response body is streamed
// unchanged, so the data plane forces identity after copying and a client value
// would never reach the upstream. Listing it here only claimed a forwarding
// that the code does not perform.
//
// X-Request-Id carries both identifiers in this repository: it is the internal
// Request ID the request log records, and the Client Correlation ID when the
// client supplied one. requestIDMiddleware keeps a client-supplied value and
// generates one otherwise, and the data plane forwards whatever the header
// holds.
var forwardableRequestHeaders = map[string]bool{
	"Accept":       true,
	"Content-Type": true,
	"Traceparent":  true,
	"Tracestate":   true,
	"X-Request-Id": true,
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
	"Content-Type":          true,
	"Content-Length":        true,
	"Content-Encoding":      true,
	"Cache-Control":         true,
	"ETag":                  true,
	"Last-Modified":         true,
	"Expires":               true,
	"Vary":                  true,
	"Retry-After":           true,
	"RateLimit-Limit":       true,
	"RateLimit-Remaining":   true,
	"RateLimit-Reset":       true,
	"X-RateLimit-Limit":     true,
	"X-RateLimit-Remaining": true,
	"X-RateLimit-Reset":     true,
	"Traceparent":           true,
	"Tracestate":            true,
	"Request-Id":            true,
	"X-Request-Id":          true,
}

// writeDirectResponse copies the upstream response to the client and stamps the
// X-Gateway-* telemetry headers the request logger reads.
//
// The headers are an internal contract between the data plane and the logging
// wrapper: they carry the winning channel, its upstream model, the band's
// priority and the attempt count, which is what the request log records. The
// names are the gateway's own.
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
	w.Header().Set(headerProvider, route.Provider)
	w.Header().Set(headerUpstreamModel, route.UpstreamModel)
	w.Header().Set(headerRoutePriority, strconv.Itoa(route.Priority))
	w.Header().Set(headerAttempts, strconv.Itoa(attempts))

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
// There is nothing to proxy to, and the honest answer is the set of models that
// can actually be routed right now.
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

// apiVersionSegment matches an API version path segment: "v" followed by
// digits and an optional beta suffix (v1, v3, v4, v1beta1). The comparison is
// case-insensitive.
//
// It deliberately does not match every segment that starts with "v" and a
// digit: /v1chat and /v2ray are application paths, not versions, and treating
// them as versions would drop the inbound /v1 the upstream expects.
var apiVersionSegment = regexp.MustCompile(`(?i)^v[0-9]+(beta[0-9]*)?$`)

// directTargetURL joins a channel's base URL with the request path.
//
// The contract is that a base URL points at the vendor's API root, including
// its version segment. The inbound path always starts with the gateway's own
// /v1 route prefix, so when the base already carries a version segment
// (/v1, /api/v3, /api/paas/v4, /compatible-mode/v1) that prefix is removed;
// keeping both would produce /api/v3/v1/chat/completions, which the vendor
// does not serve. A bare host or a non-version prefix (/gateway) has no
// version to carry, so the inbound path is left unchanged.
func directTargetURL(base string, u *url.URL) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", fmt.Errorf("base_url is empty")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("base_url %q is not a valid URL: %w", base, err)
	}
	// Build from the parsed components, not from the raw string: a legacy base
	// with "?x" or "#f" would otherwise swallow the inbound path into its own
	// query or fragment. The base's query and fragment are configuration noise,
	// not part of the API root, so they are dropped; only the inbound request's
	// query is carried over.
	suffix := u.EscapedPath()
	if lastPathSegmentIsVersion(parsed.Path) {
		suffix = strings.TrimPrefix(suffix, "/v1")
	}
	if suffix == "" {
		suffix = "/"
	}
	target := parsed.Scheme + "://" + parsed.Host + strings.TrimRight(parsed.EscapedPath(), "/") + suffix
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	if _, err := url.Parse(target); err != nil {
		return "", fmt.Errorf("base_url %q produced an invalid URL: %w", base, err)
	}
	return target, nil
}

// lastPathSegmentIsVersion reports whether a URL path ends in an API version
// segment. Only the last non-empty segment is considered: /api/v3 and
// /api/paas/v4 carry a version, while /gateway and the empty path do not.
func lastPathSegmentIsVersion(path string) bool {
	segments := strings.Split(strings.TrimRight(path, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if segments[i] != "" {
			return apiVersionSegment.MatchString(segments[i])
		}
	}
	return false
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

// attemptClass is how one upstream answer is read. It is the single definition
// of "retryable vs the request's fault", shared by the failover decision and the
// circuit breaker's failure count so the two cannot drift apart.
type attemptClass int

const (
	// attemptServed: the channel answered usefully.
	attemptServed attemptClass = iota
	// attemptFailover: the answer is channel-specific (a bad credential, a
	// missing model, a rate limit, a server error). Another line may do better,
	// and the channel is to blame.
	attemptFailover
	// attemptTerminal: the request itself is wrong, so every channel will say the
	// same. Do not fail over, and do not blame the channel.
	attemptTerminal
)

func classifyAttempt(status int) attemptClass {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusRequestTimeout, http.StatusTooManyRequests:
		return attemptFailover
	}
	if status >= 500 {
		return attemptFailover
	}
	if status >= 400 {
		return attemptTerminal
	}
	return attemptServed
}

// shouldFailover reports whether another line has a chance of doing better. A
// 400 means the request is wrong and every channel will say so, so failing over
// would only multiply the latency.
func shouldFailover(status int) bool {
	return classifyAttempt(status) == attemptFailover
}

// serveModelTestDirect runs the admin connectivity test.
//
// It walks the same route list and applies the same failover classification as
// serveDirect (classifyAttempt decides what is worth failing over), so a test
// that passes here is evidence about the path a real request could take. Two
// deliberate differences: it does not apply weighted selection — a probe is a
// deterministic walk, not a sampled request — and it does not consult the
// circuit breaker, because a probe is the operator asking a channel directly,
// including one the breaker currently holds open. The intent also differs: a
// probe wants to know which channel answered, so the winner is reported back
// rather than discarded.
func (s *Server) serveModelTestDirect(w http.ResponseWriter, r *http.Request, in modelTestRequest, payload []byte, reqID string) {
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
	// lives. The operator needs to know which channel failed, not what the
	// upstream wrote to its own log.
	var failures []string
	for _, route := range routes {
		resp, err := s.attemptDirect(probe, payload, envelope, route)
		if err != nil {
			var denied *egressDeniedError
			switch detail := clientSafeFailure(err); {
			case errors.As(err, &denied):
				// The target was resolved and then refused by egress policy — at
				// the scheme check or in the dial hook. That target is exactly
				// what the operator must fix, so log it like any other failed
				// attempt. The response keeps the address-free SafeSummary.
				reason := directFailureReason(err)
				logModelTestTarget(reqID, route, probe, reason)
				failures = append(failures, detail)
			case detail != "":
				// A missing credential fails before the target exists, so there
				// is no target to log; the detail is already the operator's fix.
				failures = append(failures, detail)
			default:
				// The dial was attempted. Report the resolved category rather
				// than flattening DNS, timeout and refusal into one word, and
				// record the target the operator configured.
				failures = recordModelTestFailure(failures, reqID, route, probe, directFailureReason(err))
			}
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			failures = recordModelTestFailure(failures, reqID, route, probe, "response could not be read")
			continue
		}
		if shouldFailover(resp.StatusCode) {
			failures = recordModelTestFailure(failures, reqID, route, probe, fmt.Sprintf("HTTP %d", resp.StatusCode))
			continue
		}
		result, err := parseProbeResponse(body, time.Since(started))
		if err != nil {
			failures = recordModelTestFailure(failures, reqID, route, probe, "response is not valid JSON")
			continue
		}
		result.Channel = route.Provider
		result.UpstreamModel = route.UpstreamModel
		writeJSON(w, http.StatusOK, result)
		return
	}

	message := "no channel could serve the test request"
	if len(failures) > 0 {
		message += ": " + strings.Join(failures, "; ")
	}
	httpx.Error(w, http.StatusBadGateway, "upstream_unavailable", message, reqID)
}

// recordModelTestFailure logs the target a failed attempt dialled and appends
// the operator-facing failure line. Every post-dial failure needs both, so the
// two cannot drift apart.
func recordModelTestFailure(failures []string, reqID string, route directRoute, probe *http.Request, reason string) []string {
	logModelTestTarget(reqID, route, probe, reason)
	return append(failures, modelTestFailureLine(route, reason))
}

// modelTestFailureLine names the channel that failed and the upstream model it
// sent. The model is operator-supplied and non-secret; reporting it keeps a
// wrong model identifier from reading like a path problem.
func modelTestFailureLine(route directRoute, reason string) string {
	if route.UpstreamModel == "" {
		return fmt.Sprintf("%s: %s", route.Provider, reason)
	}
	return fmt.Sprintf("%s (%s): %s", route.Provider, route.UpstreamModel, reason)
}

// logModelTestTarget records the target a failed probe dialled. Only scheme,
// host and path are written: the query is dropped and userinfo never appears,
// because the log is retained and read by people who are not entitled to the
// channel's configuration (ADR-0009).
func logModelTestTarget(reqID string, route directRoute, probe *http.Request, reason string) {
	target, err := directTargetURL(route.BaseURL, probe.URL)
	if err != nil {
		return
	}
	log.Printf("model test: request %s channel %s target %s: %s",
		oneLine(reqID), oneLine(route.Provider), oneLine(loggableTarget(target)), reason)
}

// loggableTarget reduces a dialled target to scheme, host and path. The query
// can carry request data and is dropped; url.URL keeps userinfo out of Host, so
// credentials embedded in a base_url cannot reach the log.
func loggableTarget(target string) string {
	u, err := url.Parse(target)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "(unparseable target)"
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// oneLine collapses a client-supplied value to a single line for logging, so a
// model name containing newlines cannot forge a log entry.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
