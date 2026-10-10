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
}
