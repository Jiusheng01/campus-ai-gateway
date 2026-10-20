package app

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The checklist is derived from counts, so the two things worth pinning down
// are which steps each role sees and that a step is not marked done by a row
// that does not actually make a model callable.
func TestBuildOnboardingGuideRoleAwareSteps(t *testing.T) {
	counts := model.OnboardingCounts{
		EnabledProviders: 1,
		EnabledModels:    1,
		RoutableModels:   1,
		Tenants:          1,
		EnabledAPIKeys:   1,
		Requests:         1,
		SampleModel:      "campus-smart",
	}

	admin := buildOnboardingGuide(false, counts)
	if len(admin.Steps) != 3 {
		t.Fatalf("gateway_admin sees %d steps, want 3", len(admin.Steps))
	}
	if !admin.Complete {
		t.Fatal("gateway_admin checklist should be complete when its three steps are done")
	}

	owner := buildOnboardingGuide(true, counts)
	if len(owner.Steps) != 6 {
		t.Fatalf("super_admin sees %d steps, want 6", len(owner.Steps))
	}
	if !owner.Complete {
		t.Fatal("super_admin checklist should be complete when all six steps are done")
	}
	if owner.SampleModel != "campus-smart" {
		t.Fatalf("sample model = %q, want the routable alias", owner.SampleModel)
	}
}

func TestBuildOnboardingGuideRouteStepNeedsARoute(t *testing.T) {
	// A catalog entry alone is not callable, so the route step must stay open
	// and the guide must not report the deployment as ready.
	guide := buildOnboardingGuide(false, model.OnboardingCounts{
		EnabledProviders: 1,
		EnabledModels:    2,
		RoutableModels:   0,
	})
	if len(guide.Steps) != 3 {
		t.Fatalf("got %d steps, want 3", len(guide.Steps))
	}
	route := guide.Steps[2]
	if route.ID != "route" || route.Done {
		t.Fatalf("route step = %#v, want a pending route step", route)
	}
	if route.Action != "models" {
		t.Fatalf("route action = %q, want models", route.Action)
	}
	if guide.Complete {
		t.Fatal("a model without a route must not complete the guide")
	}
}

func TestBuildOnboardingGuideEmptyDeployment(t *testing.T) {
	guide := buildOnboardingGuide(true, model.OnboardingCounts{})
	if guide.Complete {
		t.Fatal("an empty deployment must not report the checklist as complete")
	}
	for _, step := range guide.Steps {
		if step.Done {
			t.Fatalf("step %q is done on an empty deployment", step.ID)
		}
	}
	if guide.Steps[5].Action != "" {
		t.Fatalf("the first-request step has action %q, want none", guide.Steps[5].Action)
	}
}
