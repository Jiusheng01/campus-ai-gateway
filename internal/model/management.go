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

// Tenant is a node in the tenant hierarchy. A tenant doubles as the metering
// boundary: API keys, request logs and Redis quota counters are all keyed by
// tenant_id, so a "student" is simply a leaf tenant rather than a separate
// entity.
//
// Quota fields are pointers so that NULL can mean "inherit from the nearest
// ancestor". The three states are:
//
//	nil  -> inherit
//	0    -> explicitly unlimited
//	>0   -> this tenant's own limit
type Tenant struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Status int    `json:"status"`
	Kind   string `json:"kind"` // school | dept | teacher | student | tenant

	// Hierarchy. Path is a materialized path of UUID segments, e.g.
	// "/<root-id>/<child-id>/". It is derived from parent_id but stored so that
	// subtree and ancestor lookups are a prefix scan instead of a recursive walk.
	ParentID *string `json:"parent_id"`
	Path     string  `json:"path"`
	Depth    int     `json:"depth"`

	// Quota (the "how much" axis). See the type comment for the nil semantics.
	DailyTokenLimit     *int64 `json:"daily_token_limit"`
	MonthlyTokenLimit   *int64 `json:"monthly_token_limit"`
	MonthlyRequestLimit *int64 `json:"monthly_request_limit"`
	MonthlyBudgetMicros *int64 `json:"monthly_budget_micros"`
	// SoftTokenLimit alerts without blocking (§12.2). nil = no soft alert.
	SoftTokenLimit *int64 `json:"soft_token_limit"`

	// Access (the "which models" axis, §12.1). This is an intersection, not an
	// inheritance: a child can never widen what its parent allows.
	//
	//	nil        -> inherit (intersect with ancestors)
	//	[]         -> explicitly deny every model
	//	[a, b]     -> this tenant's own allowlist
	AllowedModels []string `json:"allowed_models"`

	// Rate (the "how fast" axis, §12.4). 0 = unlimited.
	RPMLimit int64 `json:"rpm_limit"`
	TPMLimit int64 `json:"tpm_limit"`
}

// InheritsAllowedModels reports whether this tenant defers its model allowlist
// to its ancestors. An empty-but-non-nil list means "deny everything", which is
// deliberately distinct from inheriting.
func (t Tenant) InheritsAllowedModels() bool { return t.AllowedModels == nil }

// TenantAllocation is the set of limits one level hands down to another: "how
// much" (quota) and "which models" (access). It is a full replacement, not a
// patch -- an omitted field means "inherit", which is the same thing as
// explicitly sending null.
//
// The two axes resolve differently and the fields reflect that: quota values
// inherit from the nearest ancestor that sets one, while AllowedModels is
// intersected with every ancestor so a child can never widen its parent's
// access.
type TenantAllocation struct {
	DailyTokenLimit     *int64   `json:"daily_token_limit"`
	MonthlyTokenLimit   *int64   `json:"monthly_token_limit"`
	MonthlyRequestLimit *int64   `json:"monthly_request_limit"`
	MonthlyBudgetMicros *int64   `json:"monthly_budget_micros"`
	SoftTokenLimit      *int64   `json:"soft_token_limit"`
	AllowedModels       []string `json:"allowed_models"`
}

// EffectiveLimits is the resolved view of a tenant: what it actually gets once
// inheritance has been applied, rather than what it explicitly sets. Reporting
// both is what makes a hierarchy debuggable -- "I set 5,000,000 but it stops at
// 1,000,000" has to be answerable without reading the database.
//
// soft_token_limit is deliberately absent. It is an alert threshold rather than
// a cap, nothing enforces it yet, and it does not participate in the
// inheritance walk -- reporting it next to the resolved fields would suggest a
// resolution that does not exist.
type EffectiveLimits struct {
	TenantID            string `json:"tenant_id"`
	DailyTokenLimit     int64  `json:"daily_token_limit"`
	MonthlyTokenLimit   int64  `json:"monthly_token_limit"`
	MonthlyRequestLimit int64  `json:"monthly_request_limit"`
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
}

type APIKeyAdmin struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	TenantCode     string     `json:"tenant_code"`
	TenantName     string     `json:"tenant_name"`
	Name           string     `json:"name"`
	KeyPrefix      string     `json:"key_prefix"`
	Status         int        `json:"status"`
	ExpireAt       *time.Time `json:"expire_at,omitempty"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	AllowedModels  []string   `json:"allowed_models"`
	AllowedIPs     []string   `json:"allowed_ips"`
	RPMLimit       int64      `json:"rpm_limit"`
	TPMLimit       int64      `json:"tpm_limit"`
	MaxConcurrency int64      `json:"max_concurrency"`
	RouteGroup     string     `json:"route_group"`
	CreatedAt      time.Time  `json:"created_at"`
}

type APIKeyCreateResult struct {
	APIKeyAdmin
	APIKey string `json:"api_key"`
}

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
