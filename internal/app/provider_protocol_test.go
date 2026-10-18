package app

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The glossary's word for this concept is Provider, and the console only says
// 渠道 to operators; new code and new test names use the domain term.
func anthropicProvider() model.Provider {
	return model.Provider{
		Name:          "claude",
		BaseURL:       "https://api.anthropic.com",
		Protocol:      "anthropic",
		CredentialEnv: "ANTHROPIC_API_KEY",
	}
}

// The direct data plane sends the OpenAI chat body it received straight to the
// provider. A provider marked for another wire format therefore fails on the
// first real request, which is an outage; refusing it when it is saved is a
// configuration error instead.
func TestDirectPlaneRefusesAProtocolItCannotSpeak(t *testing.T) {
	for _, protocol := range []string{"anthropic", "gemini", "openai-responses"} {
		v := anthropicProvider()
		v.Protocol = protocol
		if reason := validateProvider(&v, true); reason == "" {
			t.Fatalf("%s must be refused when the gateway dials the provider itself", protocol)
		}
	}
}

func TestDirectPlaneAcceptsTheProtocolItDoesSpeak(t *testing.T) {
	v := anthropicProvider()
	v.Protocol = "openai-chat"
	if reason := validateProvider(&v, true); reason != "" {
		t.Fatalf("openai-chat is what the direct plane speaks, got %q", reason)
	}
}

// ThinkWatch performs the translation, so the wider enum stays available there
// - with the SAME provider definition the direct mode just refused. If the two
// modes disagreed about the provider itself, the protocol field would mean two
// different things depending on deployment.
func TestThinkWatchModeAcceptsTheWiderProtocolEnum(t *testing.T) {
	for _, protocol := range []string{"openai-chat", "openai-responses", "anthropic", "gemini"} {
		v := anthropicProvider()
		v.Protocol = protocol
		if reason := validateProvider(&v, false); reason != "" {
			t.Fatalf("%s must be accepted when ThinkWatch translates, got %q", protocol, reason)
		}
	}
}

// This release narrows which protocols the direct plane will store; it does not
// change the enum or the default. An empty protocol still means the OpenAI chat
// format, and a value nobody defined is still refused in both modes.
func TestProtocolEnumAndDefaultAreUnchanged(t *testing.T) {
	empty := anthropicProvider()
	empty.Protocol = ""
	if reason := validateProvider(&empty, false); reason != "" {
		t.Fatalf("an empty protocol must be defaulted rather than refused: %q", reason)
	}
	if empty.Protocol != "openai-chat" {
		t.Fatalf("default protocol = %q, want openai-chat", empty.Protocol)
	}

	for _, direct := range []bool{true, false} {
		unknown := anthropicProvider()
		unknown.Protocol = "carrier-pigeon"
		if reason := validateProvider(&unknown, direct); reason == "" {
			t.Fatalf("an undefined protocol must be refused (direct=%v)", direct)
		}
	}
}

// The strictness has to follow the configured data plane rather than a
// hard-coded value, which is what lets a ThinkWatch deployment keep saving an
// anthropic provider. The decision is read from the same predicate the write
// handlers use, so the two cannot drift apart.
func TestProtocolStrictnessFollowsTheConfiguredDataPlane(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		refused bool
	}{
		{name: "explicit direct", cfg: Config{DataPlane: DataPlaneDirect}, refused: true},
		{name: "explicit thinkwatch", cfg: Config{DataPlane: DataPlaneThinkWatch}, refused: false},
		// A Config that never filled the field must not silently switch the
		// deployment's data plane, so the zero value stays on ThinkWatch.
		{name: "zero value stays on ThinkWatch", cfg: Config{}, refused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := anthropicProvider()
			reason := validateProvider(&v, !tc.cfg.UsesThinkWatch())
			if tc.refused && reason == "" {
				t.Fatal("expected the provider to be refused")
			}
			if !tc.refused && reason != "" {
				t.Fatalf("expected the provider to be accepted, got %q", reason)
			}
		})
	}
}
