package app

import (
	"bytes"
	"encoding/json"
)

// The usage observer exists because the response cannot be buffered whole to
// find its usage block. A streamed completion may be arbitrarily large and its
// usage arrives in the last few bytes, so a fixed capture loses exactly the
// number the quota needs. The observer keeps only the small state required to
// read usage: an SSE line at a time for streams, and a bounded body for
// non-streaming responses.
const (
	// maxUsageBody bounds the non-streaming body kept for usage parsing. A
	// non-streaming body larger than this is not inspected; the request settles
	// at its reserved estimate, which is what happened for every oversized body
	// before this observer existed.
	maxUsageBody = 1 << 20
	// maxSSELine bounds one SSE line. Usage lines are tiny; a larger line is a
	// delta carrying inline data, which cannot contain usage we need.
	maxSSELine = 1 << 20
)

type usageAccumulator struct {
	Model  string
	Input  int64
	Output int64
	Total  int64
	Seen   bool
}

type usagePayload struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	InputTokens      int64 `json:"input_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type usageResponse struct {
	Model string        `json:"model"`
	Usage *usagePayload `json:"usage"`
}

// apply reads one JSON object. It accepts both the OpenAI spelling
// (prompt/completion) and the newer input/output spelling, taking the larger of
// the two when a provider sends both. A line without usage leaves the last
// usage seen intact, because a stream's delta lines legitimately carry none.
func (a *usageAccumulator) apply(raw []byte) {
	var v usageResponse
	if json.Unmarshal(raw, &v) != nil {
		return
	}
	if v.Model != "" {
		a.Model = v.Model
	}
	if v.Usage == nil {
		return
	}
	a.Seen = true
	a.Input = v.Usage.PromptTokens
	if v.Usage.InputTokens > a.Input {
		a.Input = v.Usage.InputTokens
	}
	a.Output = v.Usage.CompletionTokens
	if v.Usage.OutputTokens > a.Output {
		a.Output = v.Usage.OutputTokens
	}
	a.Total = v.Usage.TotalTokens
	if a.Total == 0 {
		a.Total = a.Input + a.Output
	}
}

// applyDataLine consumes one SSE line, ignoring keep-alives and the terminal
// [DONE] sentinel.
func applyDataLine(acc *usageAccumulator, line []byte) {
	line = bytes.TrimSpace(line)
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
		return
	}
	acc.apply(payload)
}

func scanSSE(body []byte, acc *usageAccumulator) {
	for _, line := range bytes.Split(body, []byte("\n")) {
		applyDataLine(acc, line)
	}
}

// usageTracker observes the bytes the gateway writes to the client. It is not
// safe for concurrent use, which matches the data plane: one request is written
// by one goroutine.
type usageTracker struct {
	stream    bool
	acc       usageAccumulator
	line      []byte
	body      []byte
	truncated bool
}

func newUsageTracker(stream bool) *usageTracker {
	return &usageTracker{stream: stream}
}

// Observe is called with every chunk written to the client. It never changes
// what the client receives; a failure to parse only means the request settles
// at its estimate.
func (t *usageTracker) Observe(p []byte) {
	if t == nil {
		return
	}
	if t.stream {
		t.observeStream(p)
		return
	}
	if t.truncated {
		return
	}
	remaining := maxUsageBody - len(t.body)
	if remaining <= 0 {
		t.truncated = true
		return
	}
	if len(p) > remaining {
		t.body = append(t.body, p[:remaining]...)
		t.truncated = true
		return
	}
	t.body = append(t.body, p...)
}

func (t *usageTracker) observeStream(p []byte) {
	t.line = append(t.line, p...)
	for {
		i := bytes.IndexByte(t.line, '\n')
		if i < 0 {
			if len(t.line) > maxSSELine {
				// Drop an absurdly long line rather than holding it. A later
				// usage line is still parsed normally.
				t.line = t.line[:0]
			}
			return
		}
		applyDataLine(&t.acc, t.line[:i])
		t.line = t.line[i+1:]
	}
}

type usageResult struct {
	Model     string
	Input     int64
	Output    int64
	Total     int64
	HasUsage  bool
	Truncated bool
}

func (t *usageTracker) Result() usageResult {
	if t == nil {
		return usageResult{}
	}
	if t.stream {
		if len(t.line) > 0 {
			applyDataLine(&t.acc, t.line)
			t.line = nil
		}
	} else if !t.truncated {
		t.acc.apply(t.body)
	}
	return usageResult{
		Model:     t.acc.Model,
		Input:     t.acc.Input,
		Output:    t.acc.Output,
		Total:     t.acc.Total,
		HasUsage:  t.acc.Seen,
		Truncated: t.truncated,
	}
}

// parseUsage is the body-level parser, used by tests and by callers that
// already hold a complete body. The data plane uses usageTracker instead.
func parseUsage(body []byte, stream bool) (modelName string, input, output, total int64) {
	var acc usageAccumulator
	if stream {
		scanSSE(body, &acc)
	} else {
		acc.apply(body)
	}
	return acc.Model, acc.Input, acc.Output, acc.Total
}
