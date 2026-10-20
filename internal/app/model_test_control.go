package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/httpx"
)

type modelTestRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type modelTestResult struct {
	Model string `json:"model"`
	// Channel is the Provider (channel) that answered, and UpstreamModel is the
	// name the gateway sent it. The operator supplied the latter, so reporting
	// it is not a disclosure: it is how a wrong model identifier becomes
	// visible.
	Channel          string `json:"channel,omitempty"`
	UpstreamModel    string `json:"upstream_model,omitempty"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	LatencyMS        int64  `json:"latency_ms"`
}

// handleModelTest is the admin connectivity test: it walks the configured
// routes for one model and reports which channel answered.
func (s *Server) handleModelTest(w http.ResponseWriter, r *http.Request) {
	reqID := r.Header.Get("X-Request-Id")
	if r.Method != http.MethodPost {
		httpx.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", reqID)
		return
	}

	var in modelTestRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Request body must be valid JSON", reqID)
		return
	}
	in.Model = strings.TrimSpace(in.Model)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.Model == "" || in.Prompt == "" {
		httpx.Error(w, http.StatusBadRequest, "invalid_test_request", "model and prompt are required", reqID)
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"model": in.Model,
		"messages": []map[string]string{
			{"role": "user", "content": in.Prompt},
		},
		"stream": false,
	})

	s.serveModelTestDirect(w, r, in, payload, reqID)
}

// parseProbeResponse reduces an OpenAI-compatible completion to the fields the
// connectivity test reports.
//
// It is the single place the operator-visible meaning of "the test passed" is
// written: the token accounting and the content extraction live here, so the
// test handler and the test itself cannot drift apart.
func parseProbeResponse(body []byte, latency time.Duration) (modelTestResult, error) {
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			InputTokens      int64 `json:"input_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			OutputTokens     int64 `json:"output_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return modelTestResult{}, err
	}

	result := modelTestResult{
		Model:        out.Model,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
		TotalTokens:  out.Usage.TotalTokens,
		LatencyMS:    latency.Milliseconds(),
	}
	if out.Usage.InputTokens > result.InputTokens {
		result.InputTokens = out.Usage.InputTokens
	}
	if out.Usage.OutputTokens > result.OutputTokens {
		result.OutputTokens = out.Usage.OutputTokens
	}
	if result.TotalTokens == 0 {
		result.TotalTokens = result.InputTokens + result.OutputTokens
	}
	if len(out.Choices) > 0 {
		result.Content = out.Choices[0].Message.Content
		result.ReasoningContent = out.Choices[0].Message.ReasoningContent
	}
	return result, nil
}
