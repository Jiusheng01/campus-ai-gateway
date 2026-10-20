package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Smooth weighted round robin: weights 3:1 must yield A, A, B, A over four
// picks — the share is met over the window without ever issuing two Bs together.
func TestRouteSelectorSpreadsPicksByWeight(t *testing.T) {
	s := newRouteSelector()
	got := make([]int, 4)
	for i := range got {
		got[i] = s.start("m\x001", []int{3, 1})
	}
	want := []int{0, 0, 1, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pick sequence = %v, want %v", got, want)
		}
	}
}

// A zero (or negative) weight is neutral, not "never": a line with weight 0 is
// indistinguishable from a misconfigured one, so it becomes 1 and still gets a
// turn.
func TestRouteSelectorTreatsZeroWeightAsNeutral(t *testing.T) {
	s := newRouteSelector()
	got := make([]int, 4)
	for i := range got {
		got[i] = s.start("m\x001", []int{normalWeight(0), normalWeight(1)})
	}
	want := []int{0, 1, 0, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pick sequence = %v, want %v", got, want)
		}
	}
}

// The weights decide who leads within a band; priority decides the bands. A
// higher-priority line must never be tried before a lower-priority one.
func TestOrderRoutesKeepsPriorityBandsAndLeadsByWeight(t *testing.T) {
	s := &Server{selector: newRouteSelector()}
	routes := []directRoute{
		{Provider: "a", Priority: 1, Weight: 1},
		{Provider: "b", Priority: 1, Weight: 3},
		{Provider: "c", Priority: 2, Weight: 99},
	}

	var leads []string
	for i := 0; i < 4; i++ {
		order := s.orderRoutes("m", routes)
		if len(order) != 3 {
			t.Fatalf("order length = %d, want 3", len(order))
		}
		if order[len(order)-1].Provider != "c" {
			t.Fatalf("the priority-2 line must be tried last, got %v", order)
		}
		leads = append(leads, order[0].Provider)
	}
	// b carries weight 3 to a's 1, so it leads three of four.
	want := []string{"b", "a", "b", "b"}
	if strings.Join(leads, ",") != strings.Join(want, ",") {
		t.Fatalf("lead sequence = %v, want %v", leads, want)
	}
}

// End to end through the data plane: over four requests the first channel hit
// follows the 3:1 weights, and the attempt chain says which one started.
func TestServeDirectSpreadsStartingChannelsByWeight(t *testing.T) {
	upstream := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[],"usage":{"total_tokens":1}}`))
		}))
	}
	a := upstream()
	defer a.Close()
	b := upstream()
	defer b.Close()

	t.Setenv("TEST_WEIGHT_KEY_A", "k")
	t.Setenv("TEST_WEIGHT_KEY_B", "k")

	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"demo-model": {
			{Provider: "a", BaseURL: a.URL, UpstreamModel: "a", CredentialEnv: "TEST_WEIGHT_KEY_A", Priority: 1, Weight: 3, TimeoutMS: 5000},
			{Provider: "b", BaseURL: b.URL, UpstreamModel: "b", CredentialEnv: "TEST_WEIGHT_KEY_B", Priority: 1, Weight: 1, TimeoutMS: 5000},
		},
	}})

	var starts []string
	for i := 0; i < 4; i++ {
		rec := newAttemptRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"demo-model"}`))
		req = req.WithContext(withAttemptRecorder(req.Context(), rec))
		s.serveDirect(httptest.NewRecorder(), req)

		got := rec.list()
		if len(got) == 0 {
			t.Fatal("no attempt recorded")
		}
		starts = append(starts, got[0].Provider)
	}
	want := []string{"a", "a", "b", "a"}
	if strings.Join(starts, ",") != strings.Join(want, ",") {
		t.Fatalf("starting channels = %v, want %v", starts, want)
	}
}

// A heavier weight means a heavier fallback: if the lead fails, the band's
// remaining lines are tried heaviest first.
func TestOrderRoutesOrdersTheRestOfTheBandByWeight(t *testing.T) {
	s := &Server{selector: newRouteSelector()}
	routes := []directRoute{
		{Provider: "light", Priority: 1, Weight: 1},
		{Provider: "heavy", Priority: 1, Weight: 5},
		{Provider: "mid", Priority: 1, Weight: 3},
	}
	order := s.orderRoutes("m", routes)
	// heavy (weight 5) leads the first pick, so the remaining two are mid (3)
	// then light (1): the fallback chain is descending weight.
	if order[0].Provider != "heavy" {
		t.Fatalf("lead = %q, want heavy", order[0].Provider)
	}
	rest := order[1:]
	if rest[0].Provider != "mid" || rest[1].Provider != "light" {
		t.Fatalf("fallback order = %v, want mid then light", rest)
	}
}
