package storage

import (
	"context"
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// Credentials are a property of the database: a Provider references one row, and
// the source can switch between an environment-variable reference and a sealed
// inline value without the Provider losing its identity. These tests exercise
// the storage round trip, including the fact that the sealed value is returned
// to the data plane (it has to decrypt it) and the environment-variable name
// survives on the Provider for the console.
func TestProviderCredentialRoundTripsBySourceType(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	if _, err := pg.Pool.Exec(ctx, `DELETE FROM gateway_credential`); err != nil {
		t.Fatalf("clear gateway_credential: %v", err)
	}

	envProvider, err := pg.CreateProvider(ctx, model.Provider{
		Name:          "cred-env",
		BaseURL:       "https://env.example.test",
		Protocol:      "openai-chat",
		CredentialEnv: "TEST_STORAGE_ENV_KEY",
		Status:        1,
	})
	if err != nil {
		t.Fatalf("CreateProvider(env): %v", err)
	}
	if envProvider.CredentialSource != model.CredentialSourceEnv || envProvider.CredentialSecretRef != "TEST_STORAGE_ENV_KEY" {
		t.Fatalf("env create returned source %q ref %q", envProvider.CredentialSource, envProvider.CredentialSecretRef)
	}
	if envProvider.CredentialCiphertext != "" {
		t.Fatal("an env credential must not have a ciphertext")
	}

	sealed := "v1:sealed-value-standing-in-for-real-ciphertext"
	inlineProvider, err := pg.CreateProvider(ctx, model.Provider{
		Name:                 "cred-inline",
		BaseURL:              "https://inline.example.test",
		Protocol:             "openai-chat",
		CredentialSource:     model.CredentialSourceInline,
		CredentialCiphertext: sealed,
		Status:               1,
	})
	if err != nil {
		t.Fatalf("CreateProvider(inline): %v", err)
	}
	if inlineProvider.CredentialSource != model.CredentialSourceInline || inlineProvider.CredentialCiphertext != sealed {
		t.Fatalf("inline create returned source %q ciphertext %q", inlineProvider.CredentialSource, inlineProvider.CredentialCiphertext)
	}
	if inlineProvider.CredentialEnv != "" {
		t.Fatalf("an inline credential must not name an environment variable, got %q", inlineProvider.CredentialEnv)
	}

	providers := listedProviderByID(t, pg, envProvider.ID)
	if providers.CredentialSource != model.CredentialSourceEnv || providers.CredentialEnv != "TEST_STORAGE_ENV_KEY" {
		t.Fatalf("env read = source %q env %q", providers.CredentialSource, providers.CredentialEnv)
	}
	if providers.CredentialSecretRef != "TEST_STORAGE_ENV_KEY" || providers.CredentialCiphertext != "" {
		t.Fatalf("env read = ref %q ciphertext %q", providers.CredentialSecretRef, providers.CredentialCiphertext)
	}

	inline := listedProviderByID(t, pg, inlineProvider.ID)
	if inline.CredentialSource != model.CredentialSourceInline || inline.CredentialCiphertext != sealed {
		t.Fatalf("inline read = source %q ciphertext %q", inline.CredentialSource, inline.CredentialCiphertext)
	}
	if inline.CredentialSecretRef != "" {
		t.Fatalf("inline read must not carry an env reference, got %q", inline.CredentialSecretRef)
	}

	// Switch the inline channel to an environment-variable reference. The
	// Provider row is kept; only its Credential changes source.
	if _, err := pg.UpdateProvider(ctx, inlineProvider.ID, model.Provider{
		Name:                "cred-inline",
		BaseURL:             "https://inline.example.test",
		Protocol:            "openai-chat",
		CredentialSource:    model.CredentialSourceEnv,
		CredentialSecretRef: "TEST_STORAGE_ENV_KEY_V2",
		Status:              1,
	}); err != nil {
		t.Fatalf("UpdateProvider(switch to env): %v", err)
	}

	switched := listedProviderByID(t, pg, inlineProvider.ID)
	if switched.CredentialSource != model.CredentialSourceEnv || switched.CredentialEnv != "TEST_STORAGE_ENV_KEY_V2" {
		t.Fatalf("switched read = source %q env %q", switched.CredentialSource, switched.CredentialEnv)
	}
	if switched.CredentialCiphertext != "" {
		t.Fatal("switching to env must clear the ciphertext")
	}
}

// An update that carries no credential at all edits only the channel: the
// existing Credential must survive untouched. This is what lets the console
// leave the key field blank when an operator changes only the base URL.
func TestProviderUpdateWithoutCredentialKeepsTheExistingOne(t *testing.T) {
	ctx := context.Background()
	pg := testPostgres(t)
	clearModelsAndProviders(t, pg)
	if _, err := pg.Pool.Exec(ctx, `DELETE FROM gateway_credential`); err != nil {
		t.Fatalf("clear gateway_credential: %v", err)
	}

	created, err := pg.CreateProvider(ctx, model.Provider{
		Name:          "cred-keep",
		BaseURL:       "https://keep.example.test",
		Protocol:      "openai-chat",
		CredentialEnv: "TEST_STORAGE_KEEP_KEY",
		Status:        1,
	})
	if err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}

	if _, err := pg.UpdateProvider(ctx, created.ID, model.Provider{
		Name:     "cred-keep",
		BaseURL:  "https://keep-v2.example.test",
		Protocol: "openai-chat",
		Status:   1,
	}); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}

	got := listedProviderByID(t, pg, created.ID)
	if got.BaseURL != "https://keep-v2.example.test" {
		t.Fatalf("base_url = %q, want the edited value", got.BaseURL)
	}
	if got.CredentialSource != model.CredentialSourceEnv || got.CredentialEnv != "TEST_STORAGE_KEEP_KEY" {
		t.Fatalf("credential = source %q env %q, want the original", got.CredentialSource, got.CredentialEnv)
	}
}

func listedProviderByID(t *testing.T, pg *Postgres, id string) model.Provider {
	t.Helper()
	items, err := pg.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	for _, p := range items {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("provider %s is missing from ListProviders", id)
	return model.Provider{}
}
