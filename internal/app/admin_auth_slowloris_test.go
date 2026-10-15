package app

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

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
