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

	providers := []model.Provider{
		{
			ID:            "p1",
			Name:          "qwen",
			BaseURL:       "https://example.com/compatible-mode/v1",
			Protocol:      "openai-chat",
			CredentialEnv: "DASHSCOPE_API_KEY",
			Status:        1,
		},
		{
			ID:            "p2",
			Name:          "deepseek",
			BaseURL:       "https://api.deepseek.com/v1",
			Protocol:      "openai-chat",
			CredentialEnv: "DEEPSEEK_API_KEY",
			Status:        1,
		},
	}
	models := []model.ModelConfig{{
		ID:            "m1",
		Alias:         "campus-smart",
		ProviderID:    "p1",
		UpstreamModel: "qwen3.7-flash-2026-07-15",
		Status:        1,
	}}

	routes := map[string][]model.ModelRoute{
		"m1": {
			{ModelID: "m1", ModelAlias: "campus-smart", ProviderID: "p2", ProviderName: "deepseek", UpstreamModel: "deepseek-chat", Priority: 2, Weight: 100, Status: 1},
			{ModelID: "m1", ModelAlias: "campus-smart", ProviderID: "p1", ProviderName: "qwen", UpstreamModel: "qwen3.7-flash-2026-07-15", Priority: 1, Weight: 100, Status: 1},
		},
	}

	out, pc, ac, err := renderThinkWatchManagedConfig(existing, "tw-test", providers, models, routes)
	if err != nil {
		t.Fatal(err)
	}
	if pc != 2 || ac != 1 {
		t.Fatalf("unexpected counts providers=%d aliases=%d", pc, ac)
	}
	for _, want := range []string{
		`name: "qwen"`,
		`key: "${DASHSCOPE_API_KEY}"`,
		`name: "deepseek"`,
		`key: "${DEEPSEEK_API_KEY}"`,
		`"campus-smart":`,
		`- "qwen3.7-flash-2026-07-15"`,
		`- "deepseek-chat"`,
		`name: "campus-managed"`,
		`provider: "qwen"`,
		`provider: "deepseek"`,
		`default_route: "campus-managed"`,
		"clients:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered config missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, `provider: "qwen"`) > strings.Index(out, `provider: "deepseek"`) {
		t.Fatalf("priority order was not preserved:\n%s", out)
	}
	if strings.Contains(out, "old-alias") || strings.Contains(out, "name: old") {
		t.Fatalf("old managed config remained:\n%s", out)
	}

	// Pinned route models must be allowed for the calling gateway client, and
	// the flow-style allow list from the input must survive the rewrite.
	allow := clientAllowList(t, out, "gateway-edge/default")
	assertSameSet(t, allow, []string{
		"campus-smart",
		"qwen3.7-flash-2026-07-15",
		"deepseek-chat",
	})
	if !strings.Contains(out, "key: tw-test") {
		t.Fatalf("client key was modified:\n%s", out)
	}
}

func TestRenderThinkWatchManagedConfigRejectsEmptyEnabledModels(t *testing.T) {
	_, _, _, err := renderThinkWatchManagedConfig("version: 1\n", "", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRenderThinkWatchManagedConfigUpdatesClientAllowForPinnedRoutes covers the
// P0 regression: once routes pin concrete upstream models, ThinkWatch re-checks
// them against the gateway key's allow list, so the sync must widen it while
// preserving manual entries, keys and minimal privilege.
func TestRenderThinkWatchManagedConfigUpdatesClientAllowForPinnedRoutes(t *testing.T) {
	existing := `version: 1
listen:
  gateway:
    bind: loopback
    port: 8788
clients:
  - name: gateway-edge/default
    key: tw-test
    allow:
      - campus-smart
      - manually-kept-model
  - name: other-system
    key: tw-other
    allow:
      - unrelated-model
providers:
  - name: old
    base_url: https://old.example.com
    protocol: openai-chat
    key: old
aliases:
  old-alias: old-model
`

	providers := []model.Provider{
		{ID: "p1", Name: "qwen", BaseURL: "https://example.com/v1", Protocol: "openai-chat", CredentialEnv: "DASHSCOPE_API_KEY", Status: 1},
		{ID: "p2", Name: "deepseek", BaseURL: "https://api.deepseek.com/v1", Protocol: "openai-chat", CredentialEnv: "DEEPSEEK_API_KEY", Status: 1},
		{ID: "p3", Name: "disabled-provider", BaseURL: "https://disabled.example.com/v1", Protocol: "openai-chat", CredentialEnv: "DISABLED_API_KEY", Status: 2},
	}
	models := []model.ModelConfig{
		{ID: "m1", Alias: "campus-smart", ProviderID: "p1", UpstreamModel: "qwen3.7-flash-2026-07-15", Status: 1},
		{ID: "m2", Alias: "campus-disabled", ProviderID: "p3", UpstreamModel: "disabled-model", Status: 2},
	}
	routes := map[string][]model.ModelRoute{
		"m1": {
			{ModelID: "m1", ModelAlias: "campus-smart", ProviderID: "p1", UpstreamModel: "qwen3.7-flash-2026-07-15", Priority: 1, Weight: 100, Status: 1},
			{ModelID: "m1", ModelAlias: "campus-smart", ProviderID: "p2", UpstreamModel: "deepseek-chat", Priority: 2, Weight: 100, Status: 1},
			{ModelID: "m1", ModelAlias: "campus-smart", ProviderID: "p1", UpstreamModel: "retired-model", Priority: 3, Weight: 100, Status: 2},
		},
		"m2": {
			{ModelID: "m2", ModelAlias: "campus-disabled", ProviderID: "p3", UpstreamModel: "disabled-model", Priority: 1, Weight: 100, Status: 1},
		},
	}

	out, _, _, err := renderThinkWatchManagedConfig(existing, "tw-test", providers, models, routes)
	if err != nil {
		t.Fatal(err)
	}

	gatewayAllow := clientAllowList(t, out, "gateway-edge/default")
	assertSameSet(t, gatewayAllow, []string{
		"campus-smart",
		"manually-kept-model",
		"qwen3.7-flash-2026-07-15",
		"deepseek-chat",
	})

	// Unrelated clients keep their minimal allow list.
	otherAllow := clientAllowList(t, out, "other-system")
	assertSameSet(t, otherAllow, []string{"unrelated-model"})

	// Disabled model / route / provider must never leak into allow.
	for _, forbidden := range []string{"retired-model", "disabled-model", "campus-disabled"} {
		if strings.Contains(strings.Join(gatewayAllow, "\n"), forbidden) {
			t.Fatalf("disabled model %q leaked into allow: %v", forbidden, gatewayAllow)
		}
	}

	// Client keys must be preserved verbatim.
	if !strings.Contains(out, "key: tw-test") || !strings.Contains(out, "key: tw-other") {
		t.Fatalf("client keys were modified:\n%s", out)
	}

	// Re-running the sync must be a no-op (stable output, no duplicate entries).
	again, _, _, err := renderThinkWatchManagedConfig(out, "tw-test", providers, models, routes)
	if err != nil {
		t.Fatal(err)
	}
	if again != out {
		t.Fatalf("sync is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", out, again)
	}
}

func TestRenderThinkWatchManagedConfigAddsAllowWhenMissing(t *testing.T) {
	existing := `version: 1
clients:
  - name: gateway-edge
    key: tw-test
providers: []
`

	providers := []model.Provider{{ID: "p1", Name: "qwen", BaseURL: "https://example.com/v1", Protocol: "openai-chat", CredentialEnv: "DASHSCOPE_API_KEY", Status: 1}}
	models := []model.ModelConfig{{ID: "m1", Alias: "campus-smart", ProviderID: "p1", UpstreamModel: "qwen3.7-flash-2026-07-15", Status: 1}}
	routes := map[string][]model.ModelRoute{
		"m1": {{ModelID: "m1", ModelAlias: "campus-smart", ProviderID: "p1", UpstreamModel: "qwen3.7-flash-2026-07-15", Priority: 1, Weight: 100, Status: 1}},
	}

	out, _, _, err := renderThinkWatchManagedConfig(existing, "tw-test", providers, models, routes)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, clientAllowList(t, out, "gateway-edge"), []string{
		"campus-smart",
		"qwen3.7-flash-2026-07-15",
	})
}

func TestRenderThinkWatchManagedConfigFallsBackToAllClients(t *testing.T) {
	existing := `version: 1
clients:
  - name: gateway-edge
    key: tw-unknown
    allow:
      - campus-smart
providers: []
`

	providers := []model.Provider{{ID: "p1", Name: "qwen", BaseURL: "https://example.com/v1", Protocol: "openai-chat", CredentialEnv: "DASHSCOPE_API_KEY", Status: 1}}
	models := []model.ModelConfig{{ID: "m1", Alias: "campus-smart", ProviderID: "p1", UpstreamModel: "qwen3.7-flash-2026-07-15", Status: 1}}
	routes := map[string][]model.ModelRoute{
		"m1": {{ModelID: "m1", ModelAlias: "campus-smart", ProviderID: "p1", UpstreamModel: "qwen3.7-flash-2026-07-15", Priority: 1, Weight: 100, Status: 1}},
	}

	// When the configured gateway key matches no client, every client is
	// treated as a gateway client so the data plane keeps working.
	out, _, _, err := renderThinkWatchManagedConfig(existing, "tw-does-not-exist", providers, models, routes)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSet(t, clientAllowList(t, out, "gateway-edge"), []string{
		"campus-smart",
		"qwen3.7-flash-2026-07-15",
	})
}

// clientAllowList extracts the resolved `allow` entries of one client entry.
func clientAllowList(t *testing.T, config, name string) []string {
	t.Helper()
	lines := strings.Split(config, "\n")
	start, end := findTopLevelYAMLBlock(lines, "clients")
	if start < 0 {
		t.Fatalf("clients block missing:\n%s", config)
	}
	block := lines[start:end]

	clientIdx := -1
	clientIndent := 0
	for i, line := range block {
		if strings.TrimSpace(line) == "- name: "+name {
			clientIdx = i
			clientIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			break
		}
	}
	if clientIdx < 0 {
		t.Fatalf("client %q missing:\n%s", name, config)
	}

	allowIdx := -1
	for i := clientIdx + 1; i < len(block); i++ {
		trimmed := strings.TrimSpace(block[i])
		if trimmed == "" {
			continue
		}
		indent := len(block[i]) - len(strings.TrimLeft(block[i], " \t"))
		if strings.HasPrefix(trimmed, "- ") && indent <= clientIndent {
			break
		}
		if strings.HasPrefix(trimmed, "allow:") {
			allowIdx = i
			break
		}
	}
	if allowIdx < 0 {
		return nil
	}

	allowIndent := len(block[allowIdx]) - len(strings.TrimLeft(block[allowIdx], " \t"))
	var out []string
	for i := allowIdx + 1; i < len(block); i++ {
		trimmed := strings.TrimSpace(block[i])
		if trimmed == "" {
			break
		}
		indent := len(block[i]) - len(strings.TrimLeft(block[i], " \t"))
		if indent <= allowIndent || !strings.HasPrefix(trimmed, "- ") {
			break
		}
		out = append(out, yamlScalarValue(strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))))
	}
	return out
}

func assertSameSet(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("allow entries = %v, want %v", got, want)
	}
	seen := map[string]int{}
	for _, v := range got {
		seen[v]++
	}
	for _, v := range want {
		if seen[v] == 0 {
			t.Fatalf("allow entries = %v, missing %q (duplicates=%v)", got, v, seen)
		}
		seen[v]--
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
