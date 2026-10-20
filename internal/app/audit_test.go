package app

import (
	"net/http"
	"testing"
)

func TestParseUsageStreamingOpenAI(t *testing.T) {
	body := []byte("data: {\"model\":\"demo-model\",\"usage\":null}\n\n" +
		"data: {\"model\":\"demo-model\",\"choices\":[],\"usage\":{\"prompt_tokens\":14,\"completion_tokens\":182,\"total_tokens\":196}}\n\n" +
		"data: [DONE]\n")

	modelName, input, output, total := parseUsage(body, true)
	if modelName != "demo-model" {
		t.Fatalf("model = %q, want demo-model", modelName)
	}
	if input != 14 || output != 182 || total != 196 {
		t.Fatalf("usage = %d/%d/%d, want 14/182/196", input, output, total)
	}
}

func TestParseUsageNonStreamingResponsesShape(t *testing.T) {
	body := []byte(`{"model":"demo-model","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`)

	modelName, input, output, total := parseUsage(body, false)
	if modelName != "demo-model" {
		t.Fatalf("model = %q, want demo-model", modelName)
	}
	if input != 10 || output != 20 || total != 30 {
		t.Fatalf("usage = %d/%d/%d, want 10/20/30", input, output, total)
	}
}

func TestParseErrorCode(t *testing.T) {
	body := []byte(`{"error":{"code":"model_not_allowed","message":"no"}}`)
	if got := parseErrorCode(body); got != "model_not_allowed" {
		t.Fatalf("code = %q, want model_not_allowed", got)
	}
}


func TestParseRouteTelemetry(t *testing.T) {
	h := http.Header{}
	h.Set(headerProvider, "deepseek")
	h.Set(headerUpstreamModel, "deepseek-chat")
	h.Set(headerRoutePriority, "2")
	h.Set(headerAttempts, "2")

	got := parseRouteTelemetry(h)
	if got.Provider != "deepseek" || got.UpstreamModel != "deepseek-chat" || got.RoutePriority != 2 || got.Attempts != 2 {
		t.Fatalf("unexpected telemetry: %+v", got)
	}
}

func TestParseRouteTelemetryUnknownIsZeroValue(t *testing.T) {
	got := parseRouteTelemetry(http.Header{})
	if got.Provider != "" || got.UpstreamModel != "" || got.RoutePriority != 0 || got.Attempts != 0 {
		t.Fatalf("unexpected telemetry: %+v", got)
	}
}
