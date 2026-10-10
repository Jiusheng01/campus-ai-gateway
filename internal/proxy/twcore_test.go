package proxy

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestThinkWatchProxyStreamsAndReplacesAuthorization(t *testing.T) {
	releaseSecond := make(chan struct{})
	firstFlushed := make(chan struct{})

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
		close(firstFlushed)

		<-releaseSecond
		fmt.Fprint(w, "data: two\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	p, err := NewThinkWatchProxy(upstream.URL, "tw-test")
	if err != nil {
		t.Fatal(err)
	}

	gateway := httptest.NewServer(p)
	defer gateway.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		gateway.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"demo","stream":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer sk-campus-client")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("unexpected buffering header: %q", got)
	}

	select {
	case <-firstFlushed:
	case <-time.After(time.Second):
		t.Fatal("upstream did not flush the first SSE event")
	}

	scanner := bufio.NewScanner(resp.Body)
	firstLine := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data: ") {
				firstLine <- scanner.Text()
				return
			}
		}
		firstLine <- ""
	}()

	select {
	case line := <-firstLine:
		if line != "data: one" {
			t.Fatalf("unexpected first SSE line: %q", line)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first SSE event was buffered instead of being forwarded immediately")
	}

	close(releaseSecond)

	foundSecond := false
	for scanner.Scan() {
		if scanner.Text() == "data: two" {
			foundSecond = true
			break
		}
	}
	if !foundSecond {
		t.Fatal("second SSE event was not forwarded")
	}
}
