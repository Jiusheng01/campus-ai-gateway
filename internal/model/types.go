package model

import "time"

type APIKey struct {
	ID             string
	TenantID       string
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
}

type RequestLog struct {
	RequestID      string
	TenantID       string
	APIKeyID       string
	ClientIP       string
	RequestedModel string
	ActualModel    string
	Provider       string
	Stream         bool
	InputTokens    int64
	OutputTokens   int64
	TotalTokens    int64
	LatencyMS      int64
	StatusCode     int
	ErrorCode      string
}
