package model

import "time"

type APIKey struct {
	ID             string
	TenantID       string
	TenantPath     string
	Name           string
	KeyPrefix      string
	KeyDigest      string
	Status         int
	ExpireAt       *time.Time
	AllowedModels  []string
	AllowedIPs     []string
	RPMLimit       int64
	TPMLimit       int64
	MaxConcurrency int64
	RouteGroup     string
	// Tenant quota fields deliberately live on the resolved chain rather than
	// here: the tenant's own row is nullable (NULL = inherit) and can only be
	// turned into a concrete number by walking the ancestor chain.
}

// TenantQuota is one node of a resolved ancestor chain: the tenant id together
// with its effective (already inherited) limits. The limiter checks every
// element, so a request is rejected by whichever level runs out first.
//
// Limits use the historical convention: 0 means unlimited.
type TenantQuota struct {
	TenantID string
	// Name is carried alongside the id purely so a rejection can name the
	// level in a way an operator recognises. "level=3f2a-..." says nothing;
	// "level=租户:计算机学院" says whose budget ran out.
	Name                string
	DailyTokenLimit     int64
	MonthlyTokenLimit   int64
	MonthlyRequestLimit int64
	// MonthlyBudgetMicros is resolved alongside the token limits so the
	// effective limits reported to an administrator are complete. Nothing
	// enforces it yet (cost accounting is a separate workstream); carrying it
	// here keeps the resolution in one place instead of leaving the column
	// nullable with no reader.
	MonthlyBudgetMicros int64
}

type Identity struct {
	TenantID       string
	APIKeyID       string
	AllowedModels  map[string]struct{}
	AllowedIPs     []string
	RPMLimit       int64
	TPMLimit       int64
	MaxConcurrency int64
	RouteGroup     string

	// ModelAccessRestricted reports whether an allowlist actually applies. It
	// exists because "no allowlist" and "an empty allowlist" are different
	// things: the former permits every model (historical behaviour), the latter
	// denies every model. An empty map alone cannot tell them apart.
	ModelAccessRestricted bool

	// Effective limits for this key's own tenant (the leaf of the chain).
	DailyTokenLimit     int64
	MonthlyTokenLimit   int64
	MonthlyRequestLimit int64

	// TenantChain is the resolved ancestor chain, root first, ending with the
	// key's own tenant. Quota checks walk it so that a parent's budget caps its
	// whole subtree.
	TenantChain []TenantQuota
}

type RequestLog struct {
	RequestID      string
	TenantID       string
	APIKeyID       string
	ClientIP       string
	RequestedModel string
	ActualModel    string
	Provider       string
	UpstreamModel  string
	RoutePriority  int
	Attempts       int
	Failover       bool
	Stream         bool
	InputTokens    int64
	OutputTokens   int64
	TotalTokens    int64
	LatencyMS      int64
	StatusCode     int
	ErrorCode      string
}
