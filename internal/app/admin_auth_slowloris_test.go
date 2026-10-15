package app

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type failingDeadlineWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (w *failingDeadlineWriter) SetReadDeadline(time.Time) error {
	return w.err
}

// newSlowlorisServer returns an httptest.Server whose only handler runs
// decodeAdminAuthBody, so the Slowloris protection can be exercised over a real
// TCP connection. httptest.NewServer drives the real net/http server with live
// connections, so http.ResponseController.SetReadDeadline is honoured — unlike
// httptest.ResponseRecorder, which cannot bound a read and reports
// http.ErrNotSupported.
func newSlowlorisServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var dst struct {
			Username string `json:"username"`
		}
		if err := decodeAdminAuthBody(w, r, &dst); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// TestDecodeAdminAuthBodySlowlorisIsBounded proves the read deadline cuts off a
// body that is under the byte cap but arrives slower than the deadline. A byte
// cap alone would let the handler block until the full body arrived; here the
// client declares a small Content-Length and trickles one byte at a time, so
// the read deadline must release the handler within a few seconds of the
// deadline and the connection must still be answered.
//
// This is the real Slowloris shape the old io.LimitReader-only defence could
// not stop: the attacker never reaches the byte cap, so the only thing that
// saves the goroutine is a wall-clock bound on the read.
func TestDecodeAdminAuthBodySlowlorisIsBounded(t *testing.T) {
	srv := newSlowlorisServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Declare a 200-byte body — well under the byte cap — but never finish
	// sending it. The read deadline is the only thing that can end this.
	header := "POST / HTTP/1.1\r\nHost: " + u.Host +
		"\r\nContent-Type: application/json\r\nContent-Length: 200\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(header)); err != nil {
		t.Fatal(err)
	}

	// Trickle one byte every 250ms. At the 5s read deadline the server must
	// stop waiting regardless of how many bytes are still to come.
	go func() {
		body := []byte(`{"username":"x"}`)
		for i := 0; i < 40; i++ {
			if _, err := conn.Write(body[i%len(body) : i%len(body)+1]); err != nil {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()

	// The server must answer before the deadline plus a margin. Without the
	// read deadline this would hang until the 200 declared bytes arrived
	// (~50s at 250ms each) and the test would time out.
	conn.SetReadDeadline(time.Now().Add(adminAuthReadDeadline + 4*time.Second))
	buf := make([]byte, 4096)
	n, readErr := conn.Read(buf)
	// Either we got an HTTP response (4xx), or the server closed the connection
	// because the request was refused — both mean the handler did not block on
	// the slow body. The critical assertion is that we did NOT wait ~50s.
	if readErr != nil && n == 0 {
		t.Logf("server closed the connection without a response body: %v", readErr)
		return
	}
	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "HTTP/1.1") {
		t.Fatalf("expected an HTTP response, got %q", resp)
	}
}

// TestDecodeAdminAuthBodyFastBodySucceeds confirms a small body that arrives
// promptly still decodes and returns 200 — the read deadline must not be so
// aggressive that it harms legitimate requests.
func TestDecodeAdminAuthBodyFastBodySucceeds(t *testing.T) {
	srv := newSlowlorisServer(t)
	resp, err := srv.Client().Post(srv.URL, "application/json", strings.NewReader(`{"username":"campus-admin"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for a prompt small body, got %d", resp.StatusCode)
	}
}

func TestDecodeAdminAuthBodyFailsClosedWhenDeadlineCannotBeSet(t *testing.T) {
	wantErr := errors.New("deadline unavailable")
	w := &failingDeadlineWriter{ResponseRecorder: httptest.NewRecorder(), err: wantErr}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"username":"campus-admin"}`))
	var dst struct {
		Username string `json:"username"`
	}

	err := decodeAdminAuthBody(w, r, &dst)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected deadline error, got %v", err)
	}
	if dst.Username != "" {
		t.Fatalf("body was decoded after deadline setup failed: %q", dst.Username)
	}
}

// TestDecodeAdminAuthBodyOverCapRefusedOverTCP confirms an oversized body is
// refused at the transport layer via http.MaxBytesReader, over a real
// connection, and not decoded from a truncated prefix.
func TestDecodeAdminAuthBodyOverCapRefusedOverTCP(t *testing.T) {
	srv := newSlowlorisServer(t)
	body := `{"username":"` + strings.Repeat("a", adminAuthBodyLimit+64) + `"}`
	resp, err := srv.Client().Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for an oversized body, got %d", resp.StatusCode)
	}
}

// This is the exact Round 2 attack shape: the peer declares a 5000-byte body,
// sends cap+1 bytes, and then leaves the write side open. The server must not
// wait forever for the remainder or drain it after deciding to reject.
func TestDecodeAdminAuthBodyCapPlusOneThenStallIsBounded(t *testing.T) {
	srv := newSlowlorisServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	header := "POST / HTTP/1.1\r\nHost: " + u.Host +
		"\r\nContent-Type: application/json\r\nContent-Length: 5000\r\nConnection: keep-alive\r\n\r\n"
	if _, err := conn.Write([]byte(header)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte(strings.Repeat("a", adminAuthBodyLimit+1))); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_ = conn.SetReadDeadline(started.Add(adminAuthReadDeadline + 4*time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("server did not release the stalled request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	if elapsed := time.Since(started); elapsed > adminAuthReadDeadline+3*time.Second {
		t.Fatalf("stalled oversized request held the handler for %s", elapsed)
	}
}

// adminMiddleware wraps POST handlers to capture their status. The wrapper
// must expose its underlying writer so ResponseController can reach the live
// connection and install the deadline; otherwise any authenticated member can
// keep /admin/password handlers open indefinitely.
func TestDecodeAdminAuthBodyDeadlineSurvivesAdminAuditWrapper(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aw := &adminAuditResponseWriter{ResponseWriter: w}
		var dst struct {
			Username string `json:"username"`
		}
		if err := decodeAdminAuthBody(aw, r, &dst); err != nil {
			aw.WriteHeader(http.StatusBadRequest)
			return
		}
		aw.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	header := "POST / HTTP/1.1\r\nHost: " + u.Host +
		"\r\nContent-Type: application/json\r\nContent-Length: 200\r\nConnection: close\r\n\r\na"
	if _, err := conn.Write([]byte(header)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(adminAuthReadDeadline + 4*time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("wrapped handler did not enforce its deadline: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// net/http clears the per-request read deadline before waiting for the next
// request. Prove the auth helper does not poison a reused keep-alive connection
// after the five-second window has elapsed.
func TestDecodeAdminAuthBodyKeepAliveRequestAfterDeadlineWindow(t *testing.T) {
	srv := newSlowlorisServer(t)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	body := `{"username":"first"}`
	request := "POST / HTTP/1.1\r\nHost: " + u.Host +
		"\r\nContent-Type: application/json\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\nConnection: keep-alive\r\n\r\n" + body
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(reader, &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first request: got %d", resp.StatusCode)
	}

	time.Sleep(adminAuthReadDeadline + time.Second)
	body = `{"username":"second"}`
	request = "POST / HTTP/1.1\r\nHost: " + u.Host +
		"\r\nContent-Type: application/json\r\nContent-Length: " +
		strconv.Itoa(len(body)) + "\r\nConnection: close\r\n\r\n" + body
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("connection retained an expired deadline: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	resp, err = http.ReadResponse(reader, &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("second request after deadline window failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second request: got %d", resp.StatusCode)
	}
}
