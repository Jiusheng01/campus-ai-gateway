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

// The gateway sends the OpenAI chat body it received straight to the provider.
// A provider marked for another wire format therefore fails on the first real
// request, which is an outage; refusing it when it is saved is a configuration
// error instead.
//
// openai-chat is the only protocol that is accepted, because there is no
// translation layer in front of the channels any more: the same provider
// definition that a protocol enum once allowed through has to be refused here.
func TestProviderOnlyAcceptsTheOpenAIChatProtocol(t *testing.T) {
	for _, protocol := range []string{"openai-responses", "anthropic", "gemini"} {
		v := anthropicProvider()
		v.Protocol = protocol
		if reason := validateProvider(&v); reason == "" {
			t.Fatalf("%s must be refused when the gateway dials the provider itself", protocol)
		}
	}

	v := anthropicProvider()
	v.Protocol = "openai-chat"
	if reason := validateProvider(&v); reason != "" {
		t.Fatalf("openai-chat is what the gateway speaks, got %q", reason)
	}
}

// An empty protocol still means the OpenAI chat format, and a value nobody
// defined is still refused.
func TestProtocolDefaultAndUnknownValues(t *testing.T) {
	empty := anthropicProvider()
	empty.Protocol = ""
	if reason := validateProvider(&empty); reason != "" {
		t.Fatalf("an empty protocol must be defaulted rather than refused: %q", reason)
	}
	if empty.Protocol != "openai-chat" {
		t.Fatalf("default protocol = %q, want openai-chat", empty.Protocol)
	}

	unknown := anthropicProvider()
	unknown.Protocol = "carrier-pigeon"
	if reason := validateProvider(&unknown); reason == "" {
		t.Fatal("an undefined protocol must be refused")
	}
}
