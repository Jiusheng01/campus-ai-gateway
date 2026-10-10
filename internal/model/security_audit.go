package model

import "time"

type AdminAuditLog struct {
	ID         int64     `json:"id"`
	RequestID  string    `json:"request_id"`
	Actor      string    `json:"actor"`
	Action     string    `json:"action"`
	Resource   string    `json:"resource"`
	StatusCode int       `json:"status_code"`
	ClientIP   string    `json:"client_ip"`
	CreatedAt  time.Time `json:"created_at"`
}

type SecurityEvent struct {
	ID             int64     `json:"id"`
	RequestID      string    `json:"request_id"`
	TenantID       string    `json:"tenant_id"`
	APIKeyID       string    `json:"api_key_id"`
	EventType      string    `json:"event_type"`
	ClientIP       string    `json:"client_ip"`
	RequestedModel string    `json:"requested_model"`
	Detail         string    `json:"detail"`
	CreatedAt      time.Time `json:"created_at"`
}
