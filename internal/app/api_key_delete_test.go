package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// DELETE on an API key has two meanings now, and the default must stay the
// older, softer one: a client that omits the qualifier gets a revocation (row
// kept, history resolvable), never a row deletion.
func TestIsPurgeRequest(t *testing.T) {
	cases := []struct {
		query string
		want  bool
	}{
		{query: "", want: false},
		{query: "?purge=0", want: false},
		{query: "?purge=false", want: false},
		{query: "?purge=", want: false},
		{query: "?purge=1", want: true},
		{query: "?purge=true", want: true},
		{query: "?purge=TRUE", want: true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			u, err := url.Parse("/admin/api-keys/6f1a1b1c-0000-0000-0000-000000000000" + tc.query)
			if err != nil {
				t.Fatalf("parse url: %v", err)
			}
			req := httptest.NewRequest(http.MethodDelete, u.String(), nil)
			if got := isPurgeRequest(req); got != tc.want {
				t.Fatalf("isPurgeRequest(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}
