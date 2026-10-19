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

// The shadow chain must follow the same weighted rotation a live request would,
// or the comparison would sample one leader forever and prove nothing; and it
// must carry the whole fallback order, not just the leader.
func TestShadowChainFollowsTheWeightedRotation(t *testing.T) {
	s := newDirectTestServer(t, routeTable{models: map[string][]directRoute{
		"campus-smart": {
			{Provider: "a", UpstreamModel: "a", Priority: 1, Weight: 3},
			{Provider: "b", UpstreamModel: "b", Priority: 1, Weight: 1},
			{Provider: "c", UpstreamModel: "c", Priority: 2, Weight: 100},
		},
	}})

	var leads []string
	for i := 0; i < 4; i++ {
		chain := s.shadowChain(context.Background(), "campus-smart")
		if len(chain) != 3 {
			t.Fatalf("chain length = %d, want 3", len(chain))
		}
		for j, a := range chain {
			if a.Outcome != model.AttemptShadow {
				t.Fatalf("outcome = %q, want %q", a.Outcome, model.AttemptShadow)
			}
			if a.AttemptNo != j+1 {
				t.Fatalf("attempt_no = %d, want %d", a.AttemptNo, j+1)
			}
		}
		if chain[len(chain)-1].Provider != "c" {
			t.Fatalf("the priority-2 line must be last, got %v", chain)
		}
		leads = append(leads, chain[0].Provider)
	}
	if want := "a,a,b,a"; strings.Join(leads, ",") != want {
		t.Fatalf("shadow leaders = %v, want %v", leads, want)
	}

	if chain := s.shadowChain(context.Background(), "no-such-model"); chain != nil {
		t.Fatal("a model with no enabled route has no shadow chain")
	}
}
