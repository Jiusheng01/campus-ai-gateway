package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEstimateInputTokensEmpty(t *testing.T) {
	if got := estimateInputTokens(nil); got != 0 {
		t.Fatalf("empty body should estimate 0 tokens, got %d", got)
	}
	if got := estimateInputTokens([]byte{}); got != 0 {
		t.Fatalf("empty body should estimate 0 tokens, got %d", got)
	}
}

func TestEstimateInputTokensASCII(t *testing.T) {
	// "hello world" is 11 ASCII bytes, which is roughly 3 tokens.
	got := estimateInputTokens([]byte("hello world"))
	if got < 2 || got > 5 {
		t.Fatalf("ASCII estimate out of range: %d", got)
	}
}

// Chinese traffic is the dominant case for this gateway and was previously
// undercounted by the len(body)/4 heuristic.
func TestEstimateInputTokensChineseExceedsByteHeuristic(t *testing.T) {
	content := strings.Repeat("西安科技大学校园助手", 20) // 200 CJK runes, 600 bytes
	body := []byte(`{"model":"campus-smart","messages":[{"role":"user","content":"` + content + `"}]}`)

	got := estimateInputTokens(body)
	old := int64(len(body)/4 + 1)

	if got <= old {
		t.Fatalf("CJK-aware estimate %d should exceed the byte heuristic %d", got, old)
	}
	// 200 CJK runes should land near 200 tokens, not near 150.
	if got < 190 {
		t.Fatalf("estimate %d is too low for 200 CJK runes", got)
	}
}

func TestEstimateTPMEmptyBody(t *testing.T) {
	if got := estimateTPM(nil, requestEnvelope{}, 2048); got != 0 {
		t.Fatalf("empty body should reserve 0 tokens, got %d", got)
	}
}

// A caller that omits max_tokens used to reserve only its input size, so a
// short prompt with a long answer consumed almost nothing of its allowance.
func TestEstimateTPMReservesDefaultOutputBudget(t *testing.T) {
	body := []byte(`{"model":"campus-smart","messages":[{"role":"user","content":"hi"}]}`)
	envelope := requestEnvelope{Model: "campus-smart"}

	without := estimateTPM(body, envelope, 0)
	with := estimateTPM(body, envelope, 2048)

	if with-without != 2048 {
		t.Fatalf("default output budget should add 2048, got %d", with-without)
	}
	if with <= estimateInputTokens(body) {
		t.Fatalf("reservation %d must include an output budget", with)
	}
}

func TestEstimateTPMHonoursExplicitMaxTokens(t *testing.T) {
	body := []byte(`{"model":"campus-smart","max_tokens":16,"messages":[]}`)
	envelope := requestEnvelope{Model: "campus-smart", MaxTokens: 16}

	got := estimateTPM(body, envelope, 2048)
	if want := estimateInputTokens(body) + 16; got != want {
		t.Fatalf("explicit max_tokens must win over the default: got %d, want %d", got, want)
	}
}

func TestEstimateTPMHonoursResponsesAPIFields(t *testing.T) {
	body := []byte(`{"model":"campus-smart","max_output_tokens":64}`)
	envelope := requestEnvelope{Model: "campus-smart", MaxOutputTokens: 64}

	got := estimateTPM(body, envelope, 2048)
	if want := estimateInputTokens(body) + 64; got != want {
		t.Fatalf("max_output_tokens must be honoured: got %d, want %d", got, want)
	}
}

func TestEnsureStreamUsageInjects(t *testing.T) {
	body := []byte(`{"model":"campus-smart","stream":true,"messages":[]}`)

	out, changed := ensureStreamUsage(body, "application/json")
	if !changed {
		t.Fatal("expected stream_options to be injected")
	}

	var payload struct {
		Model         string `json:"model"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("rewritten body is not valid JSON: %v", err)
	}
	if !payload.StreamOptions.IncludeUsage {
		t.Fatal("include_usage was not set to true")
	}
	if payload.Model != "campus-smart" {
		t.Fatalf("rewriting must preserve other fields, got model %q", payload.Model)
	}
}

func TestEnsureStreamUsageKeepsCallerValue(t *testing.T) {
	body := []byte(`{"stream":true,"stream_options":{"include_usage":false}}`)

	out, changed := ensureStreamUsage(body, "application/json")
	if changed {
		t.Fatal("must not override an explicit caller stream_options")
	}
	if !bytes.Equal(out, body) {
		t.Fatal("body must be returned unchanged")
	}
}

func TestEnsureStreamUsageSkipsMultipart(t *testing.T) {
	body := []byte(`{"stream":true}`)

	if _, changed := ensureStreamUsage(body, "multipart/form-data; boundary=abc"); changed {
		t.Fatal("multipart requests must not be rewritten")
	}
}

func TestEnsureStreamUsageIgnoresInvalidJSON(t *testing.T) {
	body := []byte(`not json`)

	out, changed := ensureStreamUsage(body, "application/json")
	if changed {
		t.Fatal("invalid JSON must not be rewritten")
	}
	if !bytes.Equal(out, body) {
		t.Fatal("body must be returned unchanged")
	}
}
