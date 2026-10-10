package proxy

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThinkWatchProxyStreamsAndReplacesAuthorization(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tw-test" {
			t.Fatalf("unexpected authorization: %q", got)
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("upstream response is not flushable")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: one\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: two\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p, err := NewThinkWatchProxy(upstream.URL, "tw-test")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://gateway/v1/chat/completions", strings.NewReader(`{"model":"demo","stream":true}`))
	req.Header.Set("Authorization", "Bearer sk-campus-client")
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("unexpected buffering header: %q", got)
	}

	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data = append(data, line)
		}
	}
	if len(data) != 2 || data[0] != "data: one" || data[1] != "data: two" {
		t.Fatalf("unexpected stream body: %q", rec.Body.String())
	}
}
