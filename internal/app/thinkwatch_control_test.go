package app

import (
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func TestRenderThinkWatchManagedConfig(t *testing.T) {
	existing := `version: 1
listen:
  gateway:
    bind: loopback
    port: 8788
clients:
  - name: gateway-edge/default
    key: tw-test
    allow: [campus-smart]
providers:
  - name: old
    base_url: https://old.example.com
    protocol: openai-chat
    key: old
aliases:
  old-alias: old-model
`

	providers := []model.Provider{{
		ID:            "p1",
		Name:          "qwen",
		BaseURL:       "https://example.com/compatible-mode/v1",
		Protocol:      "openai-chat",
		CredentialEnv: "DASHSCOPE_API_KEY",
		Status:        1,
	}}
	models := []model.ModelConfig{{
		ID:            "m1",
		Alias:         "campus-smart",
		ProviderID:    "p1",
		UpstreamModel: "qwen3.7-flash-2026-07-15",
		Status:        1,
	}}

	out, pc, ac, err := renderThinkWatchManagedConfig(existing, providers, models)
	if err != nil {
		t.Fatal(err)
	}
	if pc != 1 || ac != 1 {
		t.Fatalf("unexpected counts providers=%d aliases=%d", pc, ac)
	}
	for _, want := range []string{
		`name: "qwen"`,
		`key: "${DASHSCOPE_API_KEY}"`,
		`"campus-smart": "qwen3.7-flash-2026-07-15"`,
		"clients:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "old-alias") || strings.Contains(out, "name: old") {
		t.Fatalf("old managed config remained:\n%s", out)
	}
}

func TestRenderThinkWatchManagedConfigRejectsEmptyEnabledModels(t *testing.T) {
	_, _, _, err := renderThinkWatchManagedConfig("version: 1\n", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReplaceTopLevelYAMLBlock(t *testing.T) {
	in := "version: 1\nproviders:\n  - name: old\nclients:\n  - name: keep\n"
	out := replaceTopLevelYAMLBlock(in, "providers", "providers:\n  - name: new")
	if !strings.Contains(out, "name: new") || !strings.Contains(out, "name: keep") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if strings.Contains(out, "name: old") {
		t.Fatalf("old block remained:\n%s", out)
	}
}
