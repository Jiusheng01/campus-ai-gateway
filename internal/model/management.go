package model

import "time"

type Provider struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	BaseURL       string    `json:"base_url"`
	Protocol      string    `json:"protocol"`
	CredentialEnv string    `json:"credential_env"`
	Status        int       `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type ModelConfig struct {
	ID            string    `json:"id"`
	Alias         string    `json:"alias"`
	ProviderID    string    `json:"provider_id"`
	ProviderName  string    `json:"provider_name"`
	UpstreamModel string    `json:"upstream_model"`
	Status        int       `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

type Tenant struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status int    `json:"status"`

	// Hierarchy (0014). ParentID is nil for a root; Path is the materialized
	// path "/<root>/.../<self>/" that makes a subtree a prefix compare.
	ParentID *string `json:"parent_id"`
	Path     string  `json:"path"`
	Depth    int     `json:"depth"`
	Kind     string  `json:"kind"`

	// The six enforced limits are three-state since 0014:
	//
	//	nil -> this level adds no cap; caps above it still apply
	//	  0 -> explicitly unlimited at this level
	//	 >0 -> this tenant's own cap
	//
	// They are pointers rather than int64 because "inherit" has no numeric
	// representation. A plain int64 would force the console to choose between
	// freezing a child at its parent's current value and writing 0, which
	// reads as unlimited and lets the child exceed the parent.
	DailyTokenLimit     *int64 `json:"daily_token_limit"`
	MonthlyTokenLimit   *int64 `json:"monthly_token_limit"`
	MonthlyRequestLimit *int64 `json:"monthly_request_limit"`
	RPMLimit            *int64 `json:"rpm_limit"`
	TPMLimit            *int64 `json:"tpm_limit"`
	MaxConcurrency      *int64 `json:"max_concurrency"`

	// MonthlyBudgetMicros is stored but not enforced: no price data exists, so
	// there is nothing to charge against it. It is no longer offered in the
	// console — a quota field an operator can set and nothing reads is worse
	// than an absent one, because it reads as a guarantee. Kept so the column
	// and any historical value survive until real metering lands. It also keeps
	// the two-state convention it was created with: a third state on a column
	// nothing reads would only add a distinction to explain.
	MonthlyBudgetMicros int64 `json:"monthly_budget_micros"`

	// AllowedModels is the access axis, deliberately separate from quota:
	//
	//	nil -> inherit (intersect) with ancestors
	//	[]  -> explicitly deny every model
	//	set -> this tenant's own allowlist
	//
	// Access narrows down the tree; quota inherits down the tree. Sharing one
	// resolution rule between them would make an empty allowlist mean "no
	// restriction", which is the inverted default AllModelsWildcard exists to
	// remove.
	AllowedModels []string `json:"allowed_models"`
}

// Tenant kinds. The hierarchy is school -> dept -> teacher -> student, but the
// column is free text so a deployment can add its own levels. TenantKindTenant
// is the neutral default, and is what legacy flat rows were backfilled to.
const (
	TenantKindSchool  = "school"
	TenantKindDept    = "dept"
	TenantKindTeacher = "teacher"
	TenantKindStudent = "student"
	TenantKindTenant  = "tenant"
)

// InheritsAllowedModels reports whether this tenant defers its model allowlist
// to its ancestors. An empty-but-non-nil list means "deny everything", which is
// deliberately distinct from inheriting.
func (t Tenant) InheritsAllowedModels() bool { return t.AllowedModels == nil }

// TenantAllocation is the set of limits one level hands down to another: "how
// much" (quota) and "which models" (access). It is a full replacement, not a
// patch -- an omitted field means "inherit", which is the same thing as
// explicitly sending null.
//
// The two axes resolve differently and the fields reflect that: every positive
// quota cap on the chain applies, with the tightest one winning, while
// AllowedModels is intersected with every ancestor so a child can never widen
// its parent's access.
type TenantAllocation struct {
	DailyTokenLimit     *int64   `json:"daily_token_limit"`
	MonthlyTokenLimit   *int64   `json:"monthly_token_limit"`
	MonthlyRequestLimit *int64   `json:"monthly_request_limit"`
	MonthlyBudgetMicros *int64   `json:"monthly_budget_micros"`
	RPMLimit            *int64   `json:"rpm_limit"`
	TPMLimit            *int64   `json:"tpm_limit"`
	MaxConcurrency      *int64   `json:"max_concurrency"`
	AllowedModels       []string `json:"allowed_models"`
}

// EffectiveLimits is the resolved view of a tenant: what it actually gets once
// inheritance has been applied, rather than what it explicitly sets. Reporting
// both is what makes a hierarchy debuggable -- "I set 5,000,000 but it stops at
// 1,000,000" has to be answerable without reading the database.
//
// The numbers here are concrete, with 0 meaning "no cap", which is the opposite
// convention from Tenant (where nil means inherit). That is intentional: this
// type only exists to answer "what applies", and by the time a value reaches it
// the inheritance question has already been settled.
type EffectiveLimits struct {
	TenantID            string `json:"tenant_id"`
	DailyTokenLimit     int64  `json:"daily_token_limit"`
	MonthlyTokenLimit   int64  `json:"monthly_token_limit"`
	MonthlyRequestLimit int64  `json:"monthly_request_limit"`
	RPMLimit            int64  `json:"rpm_limit"`
	TPMLimit            int64  `json:"tpm_limit"`
	MaxConcurrency      int64  `json:"max_concurrency"`
	MonthlyBudgetMicros int64  `json:"monthly_budget_micros"`
	// ModelAccessRestricted distinguishes "no allowlist applies" (every model)
	// from "an allowlist applies and it happens to be empty" (no model).
	ModelAccessRestricted bool     `json:"model_access_restricted"`
	AllowedModels         []string `json:"allowed_models"`
	Depth                 int      `json:"depth"`
	Path                  string   `json:"path"`
}

type TenantUsageStats struct {
	TenantID        string `json:"tenant_id"`
	DailyRequests   int64  `json:"daily_requests"`
	MonthlyRequests int64  `json:"monthly_requests"`
	DailyTokens     int64  `json:"daily_tokens"`
	MonthlyTokens   int64  `json:"monthly_tokens"`
	// Effective carries the limits that actually apply once inheritance has
	// been resolved, so the console can draw a bar instead of dividing by a
	// number the tenant never declared. It is nil when the caller did not ask
	// for it, which keeps the plain usage query cheap.
	Effective *EffectiveLimits `json:"effective,omitempty"`
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
