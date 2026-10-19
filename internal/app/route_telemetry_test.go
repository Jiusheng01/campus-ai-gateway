package app

import (
	"net/http"
	"testing"
)

// TestParseRouteTelemetryCurrentThinkWatchContract covers the P2 regression:
// current ThinkWatch Core reports the resolved provider name through
// X-Gateway-Upstream, which must be read as Provider (not as upstream model).
func TestParseRouteTelemetryCurrentThinkWatchContract(t *testing.T) {
	h := http.Header{}
	h.Set("X-Gateway-Upstream", "qwen")

	got := parseRouteTelemetry(h)
	if got.Provider != "qwen" {
		t.Fatalf("Provider = %q, want %q", got.Provider, "qwen")
	}
	if got.UpstreamModel != "" {
		t.Fatalf("UpstreamModel = %q, want empty (X-Gateway-Upstream is a provider name)", got.UpstreamModel)
	}
	if got.RoutePriority != 0 || got.Attempts != 0 {
		t.Fatalf("unexpected priority/attempts: %d/%d", got.RoutePriority, got.Attempts)
	}
	if got.Failover() {
		t.Fatalf("Failover must stay false without attempt telemetry")
	}
}

// TestParseRouteTelemetryFullContract keeps the forward-compatible header set
// working once ThinkWatch exposes the complete route telemetry.
func TestParseRouteTelemetryFullContract(t *testing.T) {
	h := http.Header{}
	h.Set(headerProvider, "deepseek")
	h.Set(headerUpstreamModel, "deepseek-chat")
	h.Set(headerRoutePriority, "2")
	h.Set(headerAttempts, "2")

	got := parseRouteTelemetry(h)
	if got.Provider != "deepseek" {
		t.Fatalf("Provider = %q, want %q", got.Provider, "deepseek")
	}
	if got.UpstreamModel != "deepseek-chat" {
		t.Fatalf("UpstreamModel = %q, want %q", got.UpstreamModel, "deepseek-chat")
	}
	if got.RoutePriority != 2 {
		t.Fatalf("RoutePriority = %d, want 2", got.RoutePriority)
	}
	if got.Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2", got.Attempts)
	}
	if !got.Failover() {
		t.Fatalf("Failover must be true when attempts > 1")
	}
}

func TestParseRouteTelemetryPrefersExplicitProvider(t *testing.T) {
	h := http.Header{}
	h.Set(headerProvider, "deepseek")
	h.Set("X-Gateway-Upstream", "qwen")

	if got := parseRouteTelemetry(h); got.Provider != "deepseek" {
		t.Fatalf("Provider = %q, want the explicit X-Gateway-Provider value", got.Provider)
	}
}

func TestParseRouteTelemetryIgnoresInvalidNumbers(t *testing.T) {
	h := http.Header{}
	h.Set(headerRoutePriority, "not-a-number")
	h.Set(headerAttempts, "0")

	got := parseRouteTelemetry(h)
	if got.RoutePriority != 0 || got.Attempts != 0 {
		t.Fatalf("unexpected priority/attempts: %d/%d", got.RoutePriority, got.Attempts)
	}
	if got.Failover() {
		t.Fatalf("Failover must not be guessed")
	}
}

func TestParseRouteTelemetryEmpty(t *testing.T) {
	got := parseRouteTelemetry(http.Header{})
	if got.Provider != "" || got.UpstreamModel != "" || got.RoutePriority != 0 || got.Attempts != 0 {
		t.Fatalf("unexpected telemetry: %+v", got)
	}
	if got.Failover() {
		t.Fatalf("Failover must be false without telemetry")
	}
}
