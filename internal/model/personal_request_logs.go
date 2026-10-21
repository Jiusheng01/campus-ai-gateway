package model

import "time"

// ConsoleRequestLog is the member-visible projection of one Request Record.
//
// It is deliberately narrower than RequestLogAdmin. A member needs to recognise
// and debug their own request, not to read the operator's view of the gateway:
// there is no client IP, no tenant id, no route priority, no upstream response
// content and no credential material here. `api_key_id` is included because it
// is the identifier the key filter uses and because the key may have been
// deleted since the request ran — the owner snapshot, not the key row, is what
// makes the record visible.
//
// `attempt_count` is the stored count. The attempt chain itself is only part of
// the detail projection, so a page of records does not carry every attempt of
// every row.
type ConsoleRequestLog struct {
	RequestID      string    `json:"request_id"`
	APIKeyID       string    `json:"api_key_id"`
	RequestedModel string    `json:"requested_model"`
	ActualModel    string    `json:"actual_model"`
	InputTokens    int64     `json:"input_tokens"`
	OutputTokens   int64     `json:"output_tokens"`
	TotalTokens    int64     `json:"total_tokens"`
	LatencyMS      int64     `json:"latency_ms"`
	StatusCode     int       `json:"status_code"`
	ErrorCode      string    `json:"error_code"`
	Failover       bool      `json:"failover"`
	AttemptCount   int       `json:"attempt_count"`
	CreatedAt      time.Time `json:"created_at"`
}

// ConsoleRequestLogDetail is one record plus its Routing Attempt chain.
//
// AttemptChain reuses RequestAttempt, which carries the gateway's own error
// classification and no upstream response body (ADR-0009). An empty chain is a
// real state — an uncompleted request may have no attempt rows — not an error.
type ConsoleRequestLogDetail struct {
	ConsoleRequestLog
	AttemptChain []RequestAttempt `json:"attempt_chain"`
}
