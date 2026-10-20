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
		Name:             "qwen",
		BaseURL:          "https://example.test/v1",
		Protocol:         "openai-chat",
		CredentialSecret: "sk-live-abcd1234",
	}
	if reason := validateProvider(&v); reason != "" {
		t.Fatalf("an inline credential_secret must be accepted: %q", reason)
	}
	if err := s.sealProviderCredential(&v); err != nil {
		t.Fatalf("sealProviderCredential: %v", err)
	}
	if v.CredentialSecret != "" {
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
		Name:             "qwen",
		BaseURL:          "https://example.test/v1",
		CredentialSecret: "sk-live-abc123",
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
		Name:             "qwen",
		BaseURL:          "https://example.test/v1",
		CredentialEnv:    "QWEN_API_KEY",
		CredentialSecret: "sk-live-abc123",
	}
	if reason := validateProvider(&both); reason == "" {
		t.Fatal("supplying both credential_secret and credential_env is ambiguous and must be refused")
	}

	// An update with neither means "leave the credential alone".
	update := model.Provider{Name: "qwen", BaseURL: "https://example.test/v1"}
	if reason := validateProviderForUpdate(&update); reason != "" {
		t.Fatalf("an update without a credential must be allowed: %q", reason)
	}
}

// The provider credential request field speaks the domain language: it is the
// Credential's secret, not the API Key the gateway issues to a Tenant.
func TestProviderCredentialRequestFieldIsCredentialSecret(t *testing.T) {
	raw, err := json.Marshal(model.Provider{CredentialSecret: "sk-live-abcd1234"})
	if err != nil {
		t.Fatalf("marshal provider: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"credential_secret":"sk-live-abcd1234"`) {
		t.Fatalf("the write field must be credential_secret: %s", body)
	}
	if strings.Contains(body, `"api_key"`) {
		t.Fatalf("the provider request must not carry an api_key field: %s", body)
	}

	var decoded model.Provider
	if err := json.Unmarshal([]byte(`{"credential_secret":"sk-live-abcd1234"}`), &decoded); err != nil {
		t.Fatalf("decode credential_secret: %v", err)
	}
	if decoded.CredentialSecret != "sk-live-abcd1234" {
		t.Fatalf("credential_secret was not decoded, got %q", decoded.CredentialSecret)
	}
}

// ADR-0017 promises the read side discloses at most the last four characters.
// A secret of four characters or fewer would therefore be disclosed in full, so
// anything shorter than eight is masked instead: the summary never becomes the
// secret.
func TestCredentialLast4NeverDisclosesAShortSecret(t *testing.T) {
	for _, value := range []string{"a", "abcd", "1234567"} {
		if got := credentialLast4(value); got != "****" {
			t.Fatalf("credentialLast4(%q) = %q, want the fixed mask", value, got)
		}
	}
	if got := credentialLast4("12345678"); got != "5678" {
		t.Fatalf("credentialLast4 of an eight-character secret = %q, want 5678", got)
	}
	if got := credentialLast4(""); got != "" {
		t.Fatalf("an absent secret must stay absent, got %q", got)
	}

	t.Setenv("TEST_CREDENTIAL_SHORT_KEY", "abc")
	s := &Server{}
	v := model.Provider{
		Name:                "qwen",
		BaseURL:             "https://example.test/v1",
		CredentialEnv:       "TEST_CREDENTIAL_SHORT_KEY",
		CredentialSource:    model.CredentialSourceEnv,
		CredentialSecretRef: "TEST_CREDENTIAL_SHORT_KEY",
	}
	raw, err := json.Marshal(s.providerView(v))
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, `"abc"`) {
		t.Fatalf("a three-character secret leaked into the read response: %s", body)
	}
	if !strings.Contains(body, `"last4":"****"`) {
		t.Fatalf("the summary must carry the mask, got %s", body)
	}
}

// The encryption key is the one secret that makes every inline Credential
// readable. An env-sourced channel naming it would send the sealing key to the
// channel's own base URL as a Bearer token, so both writes must refuse it while
// still accepting an ordinary vendor variable.
func TestProviderWritesRefuseTheCredentialEncryptionKey(t *testing.T) {
	validators := map[string]func(*model.Provider) string{
		"create": validateProvider,
		"update": validateProviderForUpdate,
	}
	for write, validate := range validators {
		t.Run(write, func(t *testing.T) {
			reserved := model.Provider{
				Name:          "qwen",
				BaseURL:       "https://example.test/v1",
				CredentialEnv: "CREDENTIAL_ENCRYPTION_KEY",
			}
			if reason := validate(&reserved); reason == "" {
				t.Fatal("CREDENTIAL_ENCRYPTION_KEY must not be usable as a channel credential")
			}

			vendor := model.Provider{
				Name:          "qwen",
				BaseURL:       "https://example.test/v1",
				CredentialEnv: "DASHSCOPE_API_KEY",
			}
			if reason := validate(&vendor); reason != "" {
				t.Fatalf("an ordinary vendor variable must still be accepted: %q", reason)
			}
		})
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
