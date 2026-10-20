package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
	"github.com/Jiusheng01/campus-ai-gateway/internal/security"
)

// An inline key is write-only. It is accepted on create or update, sealed before
// storage sees it, and the read view carries only a summary: source, configured
// flag and the last four characters (ADR-0017).
func TestInlineAPIKeyIsWriteOnlyAndSummarizedOnRead(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, security.CredentialKeySize)
	s := &Server{credentialSealingKey: key}

	v := model.Provider{
		Name:     "qwen",
		BaseURL:  "https://example.test/v1",
		Protocol: "openai-chat",
		APIKey:   "sk-live-abcd1234",
	}
	if reason := validateProvider(&v); reason != "" {
		t.Fatalf("an inline api_key must be accepted: %q", reason)
	}
	if err := s.sealProviderCredential(&v); err != nil {
		t.Fatalf("sealProviderCredential: %v", err)
	}
	if v.APIKey != "" {
		t.Fatal("the write-only key must be cleared before the provider reaches storage")
	}
	if v.CredentialSource != model.CredentialSourceInline || !strings.HasPrefix(v.CredentialCiphertext, "v1:") {
		t.Fatalf("sealed credential = source %q ciphertext %q", v.CredentialSource, v.CredentialCiphertext)
	}

	raw, err := json.Marshal(s.providerView(v))
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "sk-live-abcd1234") {
		t.Fatalf("the inline key leaked into the read response: %s", body)
	}
	if strings.Contains(body, v.CredentialCiphertext) {
		t.Fatalf("the sealed value leaked into the read response: %s", body)
	}
	for _, want := range []string{
		`"source_type":"inline"`,
		`"configured":true`,
		`"last4":"1234"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("read response is missing %s: %s", want, body)
		}
	}
}

// The environment-variable namespace guard applies to the env source and must
// not be applied to an inline key: a vendor key is not a variable name and is
// not resolved through os.Getenv.
func TestInlineAPIKeyBypassesTheEnvironmentNamespace(t *testing.T) {
	asEnv := model.Provider{
		Name:          "qwen",
		BaseURL:       "https://example.test/v1",
		CredentialEnv: "sk-live-abc123",
	}
	if reason := validateProvider(&asEnv); reason == "" {
		t.Fatal("a pasted key in credential_env must still be refused")
	}

	asInline := model.Provider{
		Name:    "qwen",
		BaseURL: "https://example.test/v1",
		APIKey:  "sk-live-abc123",
	}
	if reason := validateProvider(&asInline); reason != "" {
		t.Fatalf("an inline key must not be judged as an environment-variable name: %q", reason)
	}
}

func TestProviderCreateRequiresExactlyOneCredentialSource(t *testing.T) {
	none := model.Provider{Name: "qwen", BaseURL: "https://example.test/v1"}
	if reason := validateProvider(&none); reason == "" {
		t.Fatal("a channel with no credential must be refused on create")
	}

	both := model.Provider{
		Name:          "qwen",
		BaseURL:       "https://example.test/v1",
		CredentialEnv: "QWEN_API_KEY",
		APIKey:        "sk-live-abc123",
	}
	if reason := validateProvider(&both); reason == "" {
		t.Fatal("supplying both api_key and credential_env is ambiguous and must be refused")
	}

	// An update with neither means "leave the credential alone".
	update := model.Provider{Name: "qwen", BaseURL: "https://example.test/v1"}
	if reason := validateProviderForUpdate(&update); reason != "" {
		t.Fatalf("an update without a credential must be allowed: %q", reason)
	}
}

// The env source keeps returning the variable NAME (it is not a secret) while
// the value stays hidden behind the summary's last four characters.
func TestEnvironmentCredentialReadReturnsTheNameAndOnlyTheLastFour(t *testing.T) {
	t.Setenv("TEST_CREDENTIAL_READ_KEY", "sk-secret-9876")
	s := &Server{}

	v := model.Provider{
		Name:                "qwen",
		BaseURL:             "https://example.test/v1",
		Protocol:            "openai-chat",
		CredentialEnv:       "TEST_CREDENTIAL_READ_KEY",
		CredentialSource:    model.CredentialSourceEnv,
		CredentialSecretRef: "TEST_CREDENTIAL_READ_KEY",
	}
	raw, err := json.Marshal(s.providerView(v))
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, "sk-secret-9876") {
		t.Fatalf("the environment-variable value leaked into the read response: %s", body)
	}
	for _, want := range []string{
		`"credential_env":"TEST_CREDENTIAL_READ_KEY"`,
		`"source_type":"env"`,
		`"configured":true`,
		`"last4":"9876"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("read response is missing %s: %s", want, body)
		}
	}
}
