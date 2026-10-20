package model

import "time"

// Credential is the gateway-held access identity for a Provider (ADR-0017). It
// is not the API Key the gateway issues to a Tenant.
//
// SourceType decides where the secret comes from: CredentialSourceEnv means
// SecretRef names a process environment variable, and CredentialSourceInline
// means Ciphertext holds a value sealed with AES-256-GCM. At most one of
// SecretRef and Ciphertext is set.
type Credential struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	SourceType string    `json:"source_type"`
	SecretRef  string    `json:"secret_ref,omitempty"`
	Ciphertext string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// The two sources a Credential can have.
const (
	// CredentialSourceEnv resolves the secret from the process environment.
	CredentialSourceEnv = "env"
	// CredentialSourceInline holds a sealed value in the database.
	CredentialSourceInline = "inline"
)

// CredentialSummary is what the management API returns about a Provider's
// Credential: enough for the console to say "configured, ending in 1234", and
// never the secret itself (ADR-0017). Last4 is empty when the value cannot be
// read — an empty environment variable or a ciphertext this process cannot
// decrypt — rather than leaking more of it.
type CredentialSummary struct {
	SourceType string `json:"source_type"`
	Configured bool   `json:"configured"`
	Last4      string `json:"last4"`
}

// ProviderTestResult is the compact, non-content summary of the last channel
// diagnostic test. It is management metadata only: no prompt, answer, key or
// upstream body is stored here.
type ProviderTestResult struct {
	At            time.Time `json:"at"`
	OK            bool      `json:"ok"`
	StatusCode    int       `json:"status_code"`
	LatencyMS     int64     `json:"latency_ms"`
	UpstreamModel string    `json:"upstream_model"`
	ErrorKind     string    `json:"error_kind,omitempty"`
}

type Provider struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Protocol string `json:"protocol"`
	// ProviderType is a console template category (for example "qwen" or
	// "deepseek"). It is not the Vendor identity and not a runtime protocol: one
	// Vendor may be represented by several Providers, and the data plane never
	// reads this field.
	ProviderType string `json:"provider_type"`
	Description  string `json:"description"`
	// CredentialEnv is the environment-variable name for an env-sourced
	// Credential. It is a NAME, not a secret, so it is returned to the console;
	// it is empty for an inline Credential. It remains the write field for the
	// environment-variable source so existing clients keep working.
	CredentialEnv string `json:"credential_env"`
	// CredentialSecret is the write-only inline credential. It is accepted on
	// create and update, sealed before it reaches storage, and never populated on
	// a read. It is named for the Credential it becomes: the gateway-issued API
	// Key is a different concept.
	CredentialSecret string `json:"credential_secret,omitempty"`
	// Credential is the read-only summary. It is filled by the management API.
	Credential *CredentialSummary `json:"credential,omitempty"`
	Status     int                `json:"status"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`

	// LastTest is the compact result of the last channel diagnostic test, when
	// one has run. It never carries answer content.
	LastTest *ProviderTestResult `json:"last_test"`

	// List-only management counts. They are computed by the database and are
	// not part of the Provider's stored identity.
	CandidateModelCount  int `json:"candidate_model_count"`
	ReferencedRouteCount int `json:"referenced_route_count"`
	ReferencedModelCount int `json:"referenced_model_count"`

	// The resolution data the data plane needs. These fields are deliberately
	// never serialized: Ciphertext is the sealed secret, and SecretRef is carried
	// separately so the data plane does not have to special-case CredentialEnv.
	CredentialID         string `json:"-"`
	CredentialSource     string `json:"-"`
	CredentialSecretRef  string `json:"-"`
	CredentialCiphertext string `json:"-"`
}

// ProviderDeleteResult reports what one Provider delete removed.
//
// The counts are the rows that actually went away, not a prediction: the
// console shows them after the fact so an operator can see the cascade that
// happened rather than the one they expected.
type ProviderDeleteResult struct {
	DeletedRoutes         int `json:"deleted_routes"`
	DeletedVirtualModels  int `json:"deleted_virtual_models"`
	DeletedProviderModels int `json:"deleted_provider_models"`
}

// ModelConfig is a Virtual Model: its client-facing identity and lifecycle
// state. Channel and upstream model are not model state — they belong to the
// model's Routes (ADR-0005).
//
// ProviderID and UpstreamModel survive on this type only as the *write* binding
// for a create, where they name the channel and upstream model of the first
// Route to write. An update accepts neither: editing a model changes its alias
// and status only. Reads never populate them, and they are omitted from the JSON
// when empty, so a model response carries no routing field to mistake for a
// second source of truth. ProviderName is never written by a client and is
// cleared on every acknowledgement.
type ModelConfig struct {
	ID          string `json:"id"`
	Alias       string `json:"alias"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Status      int    `json:"status"`
	// ConfigVersion is the aggregate concurrency token for this model's
	// configuration. It starts at 1 and is advanced by every write that changes
	// the model or its route set.
	ConfigVersion int64     `json:"config_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// Management read projections. They are derived from the route set and
	// provider/credential state, never stored on the model.
	RouteCount           int    `json:"route_count"`
	EnabledRouteCount    int    `json:"enabled_route_count"`
	StaticallyRoutable   bool   `json:"statically_routable"`
	CredentialConfigured bool   `json:"credential_configured"`
	RoutingModeSummary   string `json:"routing_mode_summary"`

	ProviderID    string `json:"provider_id,omitempty"`
	ProviderName  string `json:"provider_name,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`
}

type ModelRoute struct {
	ID            string    `json:"id"`
	ModelID       string    `json:"model_id"`
	ModelAlias    string    `json:"model_alias"`
	ProviderID    string    `json:"provider_id"`
	ProviderName  string    `json:"provider_name"`
	UpstreamModel string    `json:"upstream_model"`
	Priority      int       `json:"priority"`
	Weight        int       `json:"weight"`
	TimeoutMS     int       `json:"timeout_ms"`
	Status        int       `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// RouteView is a management-plane route projection that adds the model and
// provider context the aggregate route page needs. It is not a second routing
// authority: every field is derived from gateway_model_route and its joins.
type RouteView struct {
	ModelRoute
	ModelDisplayName      string `json:"model_display_name"`
	ConfigVersion        int64  `json:"config_version"`
	ProviderStatus       int    `json:"provider_status"`
	CredentialConfigured bool   `json:"credential_configured"`
}

type Tenant struct {
	ID                  string `json:"id"`
	Code                string `json:"code"`
	Name                string `json:"name"`
	Status              int    `json:"status"`
	DailyTokenLimit     int64  `json:"daily_token_limit"`
	MonthlyTokenLimit   int64  `json:"monthly_token_limit"`
	MonthlyRequestLimit int64  `json:"monthly_request_limit"`
	// The minute-level rate limits live on the Tenant, not on the Key: the
	// Tenant is the rate-limit boundary (see GLOSSARY.md and ADR-0015), so every
	// key it owns shares one budget. Zero means unlimited.
	RPMLimit       int64 `json:"rpm_limit"`
	TPMLimit       int64 `json:"tpm_limit"`
	MaxConcurrency int64 `json:"max_concurrency"`
	// MonthlyBudgetMicros is stored but not enforced: no price data exists, so
	// there is nothing to charge against it. It is no longer offered in the
	// console — a quota field an operator can set and nothing reads is worse
	// than an absent one, because it reads as a guarantee. Kept so the column
	// and any historical value survive until real metering lands.
	MonthlyBudgetMicros int64 `json:"monthly_budget_micros"`
}

type TenantUsageStats struct {
	TenantID        string `json:"tenant_id"`
	DailyRequests   int64  `json:"daily_requests"`
	MonthlyRequests int64  `json:"monthly_requests"`
	DailyTokens     int64  `json:"daily_tokens"`
	MonthlyTokens   int64  `json:"monthly_tokens"`
}

type APIKeyAdmin struct {
	ID            string     `json:"id"`
	TenantID      string     `json:"tenant_id"`
	TenantCode    string     `json:"tenant_code"`
	TenantName    string     `json:"tenant_name"`
	Name          string     `json:"name"`
	KeyPrefix     string     `json:"key_prefix"`
	Status        int        `json:"status"`
	ExpireAt      *time.Time `json:"expire_at,omitempty"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	AllowedModels []string   `json:"allowed_models"`
	AllowedIPs    []string   `json:"allowed_ips"`
	// RouteGroup is reserved. Neither data plane consults it, and the console
	// no longer offers it for editing, because an input that stores a value
	// nothing reads tells the operator a constraint is in force when it is not.
	// It is still accepted on write so that editing an existing key cannot fail
	// on a field the operator never touched.
	RouteGroup string    `json:"route_group"`
	CreatedAt  time.Time `json:"created_at"`
	// Usage is the key's cumulative consumption, aggregated from the request
	// log rather than stored on the key. Keeping it derived means the number
	// cannot drift from the log it claims to summarise, and a revoked key
	// keeps reporting the traffic it actually served.
	UsageRequests int64 `json:"usage_requests"`
	UsageTokens   int64 `json:"usage_tokens"`
}

type APIKeyCreateResult struct {
	APIKeyAdmin
	APIKey string `json:"api_key"`
}

// ModelAuthorization is one Key's relationship to a model alias. Wildcard keys
// are reported separately so the console never treats removing one name from
// "*" as a valid restriction.
type ModelAuthorization struct {
	APIKeyID   string     `json:"api_key_id"`
	Name       string     `json:"name"`
	TenantID   string     `json:"tenant_id"`
	TenantName string     `json:"tenant_name"`
	Status     int        `json:"status"`
	Wildcard   bool       `json:"wildcard"`
	Allowed    bool       `json:"allowed"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

type AuthorizationResult struct {
	APIKeyID string `json:"api_key_id"`
	Outcome  string `json:"outcome"`
}

// RuntimeSummaryTotals is the redacted aggregate for one model or channel.
// SuccessRate and P95LatencyMS are computed over the same filter; no tenant,
// key or client-IP dimension is exposed here.
type RuntimeSummaryTotals struct {
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	SuccessRate  float64 `json:"success_rate"`
	P95LatencyMS float64 `json:"p95_latency_ms"`
	Failover     int64   `json:"failover"`
	Tokens       int64   `json:"tokens"`
}

// RuntimeRequest is a redacted recent request row. It deliberately has no
// tenant id, API key id or client IP.
type RuntimeRequest struct {
	RequestID      string    `json:"request_id"`
	CreatedAt      time.Time `json:"created_at"`
	RequestedModel string    `json:"requested_model"`
	ActualModel    string    `json:"actual_model"`
	Provider       string    `json:"provider"`
	UpstreamModel  string    `json:"upstream_model"`
	RoutePriority  int       `json:"route_priority"`
	StatusCode     int       `json:"status_code"`
	ErrorCode      string    `json:"error_code"`
	Attempts       int       `json:"attempts"`
	Failover       bool      `json:"failover"`
	InputTokens    int64     `json:"input_tokens"`
	OutputTokens   int64     `json:"output_tokens"`
	TotalTokens    int64     `json:"total_tokens"`
	LatencyMS      int64     `json:"latency_ms"`
}

type RuntimeAttemptOutcomes struct {
	Served int64 `json:"served"`
	Failed int64 `json:"failed"`
	Skipped int64 `json:"skipped"`
}

type ModelRuntimeSummary struct {
	Alias  string               `json:"alias"`
	From   time.Time            `json:"from"`
	To     time.Time            `json:"to"`
	Totals RuntimeSummaryTotals `json:"totals"`
	Recent []RuntimeRequest     `json:"recent"`
}

type ProviderRuntimeSummary struct {
	Provider string                  `json:"provider"`
	From     time.Time               `json:"from"`
	To       time.Time               `json:"to"`
	Totals   RuntimeSummaryTotals    `json:"totals"`
	Attempts RuntimeAttemptOutcomes  `json:"attempts"`
	Recent   []RuntimeRequest        `json:"recent"`
}

// ProviderImpact is a server-computed preview of a channel delete. It is not
// the final authority: DELETE rechecks and returns what actually happened.
type ProviderImpactModel struct {
	ModelID          string `json:"model_id"`
	Alias            string `json:"alias"`
	RoutesToProvider int    `json:"routes_to_provider"`
	RemainingRoutes  int    `json:"remaining_routes"`
	Orphaned         bool   `json:"orphaned"`
}

type ProviderImpact struct {
	Routes            int                   `json:"routes"`
	ProviderModels    int                   `json:"provider_models"`
	OrphanedModels    int                   `json:"orphaned_models"`
	AffectedModels    []ProviderImpactModel `json:"affected_models"`
}

// ModelImpact is the disable preview: it never rewrites Keys.
type ModelImpact struct {
	Alias            string `json:"alias"`
	EnabledRoutes    int    `json:"enabled_routes"`
	EnabledProviders int    `json:"enabled_providers"`
	ExplicitKeys     int    `json:"explicit_keys"`
	WildcardKeys     int    `json:"wildcard_keys"`
}

// RequestAttempt is one Routing Attempt under a Request Record.
//
// It exists because the Request Record keeps only the winning channel and a
// count, which cannot say what was tried and why the gateway moved on. Outcome
// is "served", "failed" or "skipped". ErrorKind is the gateway's own
// classification (never upstream text). No upstream response content is stored
// (ADR-0009).
type RequestAttempt struct {
	AttemptNo     int    `json:"attempt_no"`
	Provider      string `json:"provider"`
	UpstreamModel string `json:"upstream_model"`
	Outcome       string `json:"outcome"`
	StatusCode    int    `json:"status_code"`
	LatencyMS     int64  `json:"latency_ms"`
	ErrorKind     string `json:"error_kind"`
}

// The outcomes a Routing Attempt can have.
const (
	// AttemptServed: the channel answered and its response reached the client.
	AttemptServed = "served"
	// AttemptFailed: the channel was tried and did not serve (transport error or
	// a failover-eligible status).
	AttemptFailed = "failed"
	// AttemptSkipped: the channel was not tried — the circuit breaker had it open.
	AttemptSkipped = "skipped"
)

type RequestLogAdmin struct {
	RequestID      string    `json:"request_id"`
	TenantID       string    `json:"tenant_id"`
	APIKeyID       string    `json:"api_key_id"`
	ClientIP       string    `json:"client_ip"`
	RequestedModel string    `json:"requested_model"`
	ActualModel    string    `json:"actual_model"`
	Provider       string    `json:"provider"`
	UpstreamModel  string    `json:"upstream_model"`
	RoutePriority  int       `json:"route_priority"`
	Attempts       int       `json:"attempts"`
	Failover       bool      `json:"failover"`
	Stream         bool      `json:"stream"`
	InputTokens    int64     `json:"input_tokens"`
	OutputTokens   int64     `json:"output_tokens"`
	TotalTokens    int64     `json:"total_tokens"`
	LatencyMS      int64     `json:"latency_ms"`
	StatusCode     int       `json:"status_code"`
	ErrorCode      string    `json:"error_code"`
	CreatedAt      time.Time `json:"created_at"`
}

type DashboardStats struct {
	TodayRequests int64   `json:"today_requests"`
	TodayTokens   int64   `json:"today_tokens"`
	TodayErrors   int64   `json:"today_errors"`
	AvgLatencyMS  float64 `json:"avg_latency_ms"`
	P95LatencyMS  float64 `json:"p95_latency_ms"`
	FailoverCount int64   `json:"failover_count"`
}

type DashboardConfigCounts struct {
	Models           int64 `json:"models"`
	EnabledModels    int64 `json:"enabled_models"`
	Providers        int64 `json:"providers"`
	EnabledProviders int64 `json:"enabled_providers"`
}

type ProviderStats struct {
	Provider     string  `json:"provider"`
	Requests     int64   `json:"requests"`
	SuccessRate  float64 `json:"success_rate"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
}

// RequestLogSummary is the aggregate for one filter selection. It answers the
// question the paginated table cannot: "how much traffic does this filter
// actually cover", without the operator having to page through it.
//
// Every field is computed by the database over the whole filtered set, not over
// the returned page, so the numbers do not change when the page does.
type RequestLogSummary struct {
	Requests      int64   `json:"requests"`
	Errors        int64   `json:"errors"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	TotalTokens   int64   `json:"total_tokens"`
	AvgLatencyMS  float64 `json:"avg_latency_ms"`
	P95LatencyMS  float64 `json:"p95_latency_ms"`
	FailoverCount int64   `json:"failover_count"`
}

// RequestLogUsage is the usage view for one filter selection: the same totals
// as the summary, the tenant's quota consumption against its limits, and the
// aggregated usage per API key. The totals and the by-key grouping come from
// the exact predicate the detail page and the summary use, so the three always
// describe the same set of rows.
type RequestLogUsage struct {
	Totals RequestLogUsageTotals `json:"totals"`
	ByKey  []APIKeyUsage         `json:"by_key"`
}

// RequestLogUsageTotals is the summary plus the quota bands. It embeds
// RequestLogSummary rather than repeating its eight fields so the totals here
// cannot drift from the summary endpoint's: there is one definition of what the
// aggregate for a filter selection is, and this type extends it. The embedded
// struct is inlined on the wire, so the JSON is exactly the summary's fields
// followed by quota.
type RequestLogUsageTotals struct {
	RequestLogSummary
	Quota RequestLogQuota `json:"quota"`
}

// RequestLogQuota is the tenant quota share for one filter selection. Each
// dimension is nil when that dimension has no configured limit (limit <= 0), so
// the console can render "not set" instead of a misleading 0%.
type RequestLogQuota struct {
	DailyToken     *QuotaDimension `json:"daily_token"`
	MonthlyToken   *QuotaDimension `json:"monthly_token"`
	MonthlyRequest *QuotaDimension `json:"monthly_request"`
}

// QuotaDimension is one quota dimension: what the tenant has used (derived from
// the request log for the day/month), what it is limited to, and the percentage
// of the limit that used represents.
type QuotaDimension struct {
	Used  int64   `json:"used"`
	Limit int64   `json:"limit"`
	Pct   float64 `json:"pct"`
}

// APIKeyUsage is one API key's share of the filtered usage, and what fraction of
// its tenant's monthly token limit that represents.
//
// QuotaShare is a *float64 rather than a float because a key whose tenant has no
// monthly token limit cannot report a meaningful share; the console renders that
// as "not set", matching the null the totals return for an unset limit.
type APIKeyUsage struct {
	APIKeyID   string   `json:"api_key_id"`
	Name       string   `json:"name"`
	KeyPrefix  string   `json:"key_prefix"`
	Requests   int64    `json:"requests"`
	Tokens     int64    `json:"tokens"`
	QuotaShare *float64 `json:"quota_share"`
}

// RequestLogBucket is one point of the traffic trend.
//
// Bucket is a wall-clock label produced by the database in the caller's UTC
// offset, not an instant: it is rendered verbatim on the chart axis, so a
// timestamp the browser would re-shift into another hour is exactly what must
// not be returned here.
type RequestLogBucket struct {
	Bucket       string  `json:"bucket"`
	Requests     int64   `json:"requests"`
	Tokens       int64   `json:"tokens"`
	Errors       int64   `json:"errors"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
	P95LatencyMS float64 `json:"p95_latency_ms"`
}

// TenantFacet is a tenant that appears in the request log.
type TenantFacet struct {
	ID   string `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// APIKeyFacet is an API key that appears in the request log. TenantID lets the
// console narrow the key list once a tenant is chosen.
type APIKeyFacet struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	KeyPrefix string `json:"key_prefix"`
	TenantID  string `json:"tenant_id"`
}

// RequestLogFacets is the set of values a request-log filter can take.
//
// It is derived from the log rather than from the tenant and key tables, for
// two reasons. The list stays short enough to be usable — a deployment with
// hundreds of keys would otherwise offer every one of them, most of which have
// never served a request — and the access it needs is exactly the access the
// log itself needs, so a role that may read logs can filter them without also
// being granted the tenant and key management APIs.
type RequestLogFacets struct {
	Tenants []TenantFacet `json:"tenants"`
	Keys    []APIKeyFacet `json:"keys"`
}
