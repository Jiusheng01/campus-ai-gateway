package app

import (
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The model allow-list used to be an allow-list of four paths, so every other
// endpoint under /v1 was forwarded without a model name — and therefore without
// the per-key allow-list. These are the routes that must now demand one, plus
// the one route that legitimately has none.
func TestRequiresModelFailsClosed(t *testing.T) {
	withModel := []string{
		"/v1/chat/completions",
		"/v1/responses",
		"/v1/embeddings",
		"/v1/audio/transcriptions",
		"/v1/audio/speech",
		"/v1/images/generations",
		"/v1/completions",
		// A route that does not exist yet must inherit the rule rather than
		// wait to be added to a list.
		"/v1/some/future/endpoint",
	}
	for _, path := range withModel {
		if !requiresModel(path) {
			t.Fatalf("%s must require a model", path)
		}
	}
	if requiresModel("/v1/models") {
		t.Fatal("/v1/models carries no model field and must not require one")
	}
	if requiresModel("/healthz") {
		t.Fatal("non-/v1 paths are not the gateway's business")
	}
}

// An empty allowed_models list is a decision to allow nothing. The whole point
// of the wildcard is that this case can no longer be read as "unrestricted".
func TestIdentityAllowsModelTreatsEmptyAsDeny(t *testing.T) {
	restrictedToNothing := model.Identity{AllowedModels: map[string]struct{}{}}
	if restrictedToNothing.AllowsModel("campus-smart") {
		t.Fatal("an empty allow-list must deny every model")
	}

	one := model.Identity{AllowedModels: map[string]struct{}{"campus-smart": {}}}
	if !one.AllowsModel("campus-smart") {
		t.Fatal("a listed model must be allowed")
	}
	if one.AllowsModel("campus-smart-2") {
		t.Fatal("an unlisted model must be denied")
	}

	everything := model.Identity{AllModels: true}
	if !everything.AllowsModel("anything-at-all") {
		t.Fatal("the wildcard must allow every model")
	}
}

func TestValidateCredentialEnvRejectsGatewayOwnedVariables(t *testing.T) {
	rejected := []string{
		"ADMIN_SESSION_SECRET",
		"ADMIN_PROXY_SECRET",
		"API_KEY_HMAC_SECRET",
		"THINKWATCH_GATEWAY_KEY",
		"DATABASE_URL",
		"REDIS_PASSWORD",
		"TWCORE_EXE",
		"DATA_PLANE",
		"GATEWAY_ANYTHING",
		// Not a variable name at all: pasting the key itself would put a
		// secret in the database, which the indirection exists to avoid.
		"sk-live-abc123",
		"lowercase_name",
		"",
	}
	for _, name := range rejected {
		if reason := validateCredentialEnv(name); reason == "" {
			t.Fatalf("%q must be rejected", name)
		}
	}

	accepted := []string{
		"DASHSCOPE_API_KEY",
		"DEEPSEEK_API_KEY",
		"OPENAI_API_KEY",
		"ARK_API_KEY",
		"ZHIPU_API_KEY",
	}
	for _, name := range accepted {
		if reason := validateCredentialEnv(name); reason != "" {
			t.Fatalf("%q must be accepted, got %q", name, reason)
		}
	}
}

func TestValidateProviderRestrictsProtocolToOpenAIChatInDirectMode(t *testing.T) {
	anthropic := model.Provider{
		Name:          "claude",
		BaseURL:       "https://api.anthropic.com",
		Protocol:      "anthropic",
		CredentialEnv: "ANTHROPIC_API_KEY",
	}
	if reason := validateProvider(&anthropic, true); reason == "" {
		t.Fatal("the direct data plane speaks OpenAI only, so an anthropic channel must be refused")
	}

	same := model.Provider{
		Name:          "claude",
		BaseURL:       "https://api.anthropic.com",
		Protocol:      "anthropic",
		CredentialEnv: "ANTHROPIC_API_KEY",
	}
	if reason := validateProvider(&same, false); reason != "" {
		t.Fatalf("ThinkWatch performs the translation, so the protocol must be accepted there: %q", reason)
	}
}

func TestValidateProviderRejectsAGatewaySecretAsCredential(t *testing.T) {
	v := model.Provider{
		Name:          "attacker",
		BaseURL:       "https://attacker.example",
		Protocol:      "openai-chat",
		CredentialEnv: "ADMIN_PROXY_SECRET",
	}
	reason := validateProvider(&v, true)
	if !strings.Contains(reason, "gateway-owned") {
		t.Fatalf("the rejection must explain why, got %q", reason)
	}
}

func TestNormalizeAllowedModels(t *testing.T) {
	t.Run("nil becomes the empty deny-list", func(t *testing.T) {
		var list []string
		if reason := normalizeAllowedModels(&list); reason != "" {
			t.Fatalf("unexpected rejection: %q", reason)
		}
		if list == nil || len(list) != 0 {
			t.Fatalf("want an empty non-nil list, got %#v", list)
		}
	})

	t.Run("the wildcard stands alone", func(t *testing.T) {
		list := []string{model.AllModelsWildcard}
		if reason := normalizeAllowedModels(&list); reason != "" {
			t.Fatalf("unexpected rejection: %q", reason)
		}
		if len(list) != 1 || list[0] != model.AllModelsWildcard {
			t.Fatalf("want the wildcard, got %#v", list)
		}
	})

	t.Run("wildcard plus names is ambiguous", func(t *testing.T) {
		list := []string{model.AllModelsWildcard, "campus-smart"}
		if reason := normalizeAllowedModels(&list); reason == "" {
			t.Fatal("a list that says both everything and one model has no single reading")
		}
	})

	t.Run("duplicates are dropped and the order is stable", func(t *testing.T) {
		list := []string{" b ", "a", "b", "a"}
		if reason := normalizeAllowedModels(&list); reason != "" {
			t.Fatalf("unexpected rejection: %q", reason)
		}
		want := []string{"a", "b"}
		if len(list) != len(want) || list[0] != want[0] || list[1] != want[1] {
			t.Fatalf("got %#v, want %#v", list, want)
		}
	})

	t.Run("an empty entry is a mistake, not an omission", func(t *testing.T) {
		list := []string{"campus-smart", "  "}
		if reason := normalizeAllowedModels(&list); reason == "" {
			t.Fatal("a blank model name must be refused")
		}
	})
}
