package app

import "testing"

func TestParseUsageStreamingOpenAI(t *testing.T) {
	body := []byte("data: {\"model\":\"campus-smart\",\"usage\":null}\n\n" +
		"data: {\"model\":\"campus-smart\",\"choices\":[],\"usage\":{\"prompt_tokens\":14,\"completion_tokens\":182,\"total_tokens\":196}}\n\n" +
		"data: [DONE]\n")

	modelName, input, output, total := parseUsage(body, true)
	if modelName != "campus-smart" {
		t.Fatalf("model = %q, want campus-smart", modelName)
	}
	if input != 14 || output != 182 || total != 196 {
		t.Fatalf("usage = %d/%d/%d, want 14/182/196", input, output, total)
	}
}

func TestParseUsageNonStreamingResponsesShape(t *testing.T) {
	body := []byte(`{"model":"campus-smart","usage":{"input_tokens":10,"output_tokens":20,"total_tokens":30}}`)

	modelName, input, output, total := parseUsage(body, false)
	if modelName != "campus-smart" {
		t.Fatalf("model = %q, want campus-smart", modelName)
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
