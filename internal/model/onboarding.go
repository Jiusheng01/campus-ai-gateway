package model

// OnboardingCounts is the raw state the setup guide is derived from. It is
// deliberately a set of counts rather than a stored progress record: the guide
// must describe what the deployment currently has, not what an operator once
// clicked through. Deleting the last route therefore reopens the route step
// without anyone resetting a flag.
type OnboardingCounts struct {
	Providers        int64
	EnabledProviders int64
	Models           int64
	EnabledModels    int64
	// RoutableModels counts enabled models that have at least one enabled route.
	// A model row with no route is valid configuration but cannot serve traffic,
	// which is exactly the state the guide has to point out.
	RoutableModels int64
	Tenants        int64
	EnabledAPIKeys int64
	Requests       int64
	// SampleModel is an enabled, routable alias used in the curl example. Empty
	// when none exists.
	SampleModel string
}

// OnboardingStep is one item in the console's setup checklist.
type OnboardingStep struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Done        bool   `json:"done"`
	Count       int64  `json:"count"`
	// Action is the console page that completes the step. Empty for the final
	// verification step, which has no dedicated page.
	Action string `json:"action,omitempty"`
}

// OnboardingGuide is the role-aware checklist returned to the dashboard.
type OnboardingGuide struct {
	Steps       []OnboardingStep `json:"steps"`
	Complete    bool             `json:"complete"`
	SampleModel string           `json:"sample_model,omitempty"`
}
