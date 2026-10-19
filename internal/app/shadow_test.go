package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func TestResolveDataPlaneShadow(t *testing.T) {
	if got, err := resolveDataPlane("shadow", "http://tw.test"); err != nil || got != DataPlaneShadow {
		t.Fatalf("shadow with a URL: got %q err %v", got, err)
	}
	if _, err := resolveDataPlane("shadow", ""); err == nil {
		t.Fatal("shadow without THINKWATCH_URL must be a startup error")
	}

	cfg := Config{DataPlane: DataPlaneShadow}
	if !cfg.UsesThinkWatch() {
		t.Fatal("shadow serves through ThinkWatch, so UsesThinkWatch must be true")
	}
	if !cfg.ShadowDirect() {
		t.Fatal("shadow must report ShadowDirect")
	}
	if cfg.DataPlaneMode() != DataPlaneShadow {
		t.Fatalf("mode = %q, want %q", cfg.DataPlaneMode(), DataPlaneShadow)
	}
}

// The shadow decision must follow the same weighted rotation a live request
// would, or the comparison would sample one leader forever and prove nothing.
func TestShadowDecisionFollowsTheWeightedRotation(t *testing.T) {
	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "a", UpstreamModel: "a", Priority: 1, Weight: 3},
			{Provider: "b", UpstreamModel: "b", Priority: 1, Weight: 1},
		},
	}})

	var got []string
	for i := 0; i < 4; i++ {
		d, ok := s.shadowDecision(context.Background(), "campus-smart")
		if !ok {
			t.Fatal("a configured model must have a shadow decision")
		}
		if d.Outcome != model.AttemptShadow {
			t.Fatalf("outcome = %q, want %q", d.Outcome, model.AttemptShadow)
		}
		got = append(got, d.Provider)
	}
	if want := "a,a,b,a"; strings.Join(got, ",") != want {
		t.Fatalf("shadow decisions = %v, want %v", got, want)
	}

	if _, ok := s.shadowDecision(context.Background(), "no-such-model"); ok {
		t.Fatal("a model with no enabled route has no shadow decision")
	}
}
