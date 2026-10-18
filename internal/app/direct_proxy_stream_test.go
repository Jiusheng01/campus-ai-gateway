package app

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The response-header allow list must not break streaming. An SSE completion
// arrives as text/event-stream with no Content-Length and several events spread
// over time; the gateway has to forward the content type, keep the connection
// open, and deliver each event as the upstream flushes it.
func TestServeDirectStreamsSSEAndKeepsItsContentType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Set-Cookie", "leak=1")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: {\"chunk\":%d}\n\n", i)
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	t.Setenv("TEST_DIRECT_KEY_SSE", "sk-sse")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "qwen", BaseURL: upstream.URL + "/v1", UpstreamModel: "qwen-plus", CredentialEnv: "TEST_DIRECT_KEY_SSE", Priority: 1},
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"campus-smart","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.serveDirect(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("the stream content type was lost: %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("the streaming cache hint was lost: %q", got)
	}
	if got := rec.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("a cookie leaked through the stream path: %q", got)
	}

	// Every event must have reached the client. httptest.ResponseRecorder
	// buffers, but the handler only returns once the upstream body closes, so
	// the full stream is present here.
	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	var events []string
	for scanner.Scan() {
		if line := scanner.Text(); strings.HasPrefix(line, "data:") {
			events = append(events, line)
		}
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 SSE events, got %d: %q", len(events), rec.Body.String())
	}
	if events[2] != `data: {"chunk":2}` {
		t.Fatalf("last event = %q", events[2])
	}
}
