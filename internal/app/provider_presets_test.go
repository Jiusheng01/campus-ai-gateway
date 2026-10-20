package app

import (
	"strings"
	"testing"
)

// A channel created through the API gets its common models seeded from the
// server-side catalog. The console's templates are one client of this, not the
// source of truth: the seeding has to work for a client that never loads them.
func TestPresetProviderModelsComeFromTheServerSideCatalog(t *testing.T) {
	deepseek := presetProviderModels("DeepSeek", "")
	if !containsModel(deepseek, "deepseek-chat") || !containsModel(deepseek, "deepseek-reasoner") {
		t.Fatalf("deepseek preset = %v, want deepseek-chat and deepseek-reasoner", deepseek)
	}

	qwen := presetProviderModels(" qwen ", "")
	if !containsModel(qwen, "qwen-max") {
		t.Fatalf("qwen preset = %v, want qwen-max", qwen)
	}

	// A template's alternative stored identifiers must resolve to the same list.
	if got := presetProviderModels("volcengine-ark", ""); len(got) == 0 || !containsModel(got, presetProviderModels("doubao", "")[0]) {
		t.Fatalf("the volcengine-ark alias must share the doubao preset, got %v", got)
	}

	if got := presetProviderModels("custom-openai-compatible", ""); len(got) != 0 {
		t.Fatalf("an unknown supplier must not inherit another supplier's models, got %v", got)
	}
}

// Every supplier the console offers a template for must also have a preset on
// the server. Otherwise the first hop depends on which client created the
// channel.
func TestEveryConsoleTemplateHasAServerPreset(t *testing.T) {
	for _, id := range []string{"qwen", "deepseek", "openai", "glm", "kimi", "doubao"} {
		if len(presetProviderModels(id, "")) == 0 {
			t.Errorf("supplier %q has no server-side preset models", id)
		}
	}
}

// The catalog is returned by value: a caller that trims or reorders the seed
// must not corrupt the next channel's preset.
func TestPresetProviderModelsReturnsACopy(t *testing.T) {
	first := presetProviderModels("deepseek", "")
	if len(first) == 0 {
		t.Fatal("premise: deepseek has a preset")
	}
	first[0] = "mutated"

	second := presetProviderModels("deepseek", "")
	if strings.EqualFold(second[0], "mutated") {
		t.Fatal("presetProviderModels handed out the catalog's own slice")
	}
}

// The base URL is what identifies the supplier, because the channel name is
// only a prefill the operator may change after choosing a template. A renamed
// DeepSeek channel still gets DeepSeek's presets; an unrecognised endpoint gets
// none rather than inheriting another supplier's list from a coincidental name.
func TestPresetProviderModelsResolveFromTheBaseURL(t *testing.T) {
	renamed := presetProviderModels("prod-east-channel", "https://api.deepseek.com/v1")
	if !containsModel(renamed, "deepseek-chat") {
		t.Fatalf("a renamed channel on the DeepSeek base URL must be seeded, got %v", renamed)
	}

	// Normalisation: case, a trailing slash and a query string do not change
	// which endpoint the base URL names.
	normalised := presetProviderModels("", "HTTPS://API.DEEPSEEK.COM/v1/?region=cn")
	if !containsModel(normalised, "deepseek-chat") {
		t.Fatalf("the base URL must be normalised before lookup, got %v", normalised)
	}

	if got := presetProviderModels("campus-internal", "https://internal.example.test/v2"); len(got) != 0 {
		t.Fatalf("an unknown base URL must not inherit presets, got %v", got)
	}

	// The name remains a fallback for a client that created a channel without a
	// recognisable (or with an empty) base URL.
	if got := presetProviderModels("deepseek", ""); !containsModel(got, "deepseek-chat") {
		t.Fatalf("the name fallback must still seed a known supplier, got %v", got)
	}
}

func containsModel(list []string, want string) bool {
	for _, name := range list {
		if name == want {
			return true
		}
	}
	return false
}
