package app

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// A create writes the model's first Route, so the channel and upstream model
// are required there: a model created without one could never serve traffic.
func TestValidateModelCreateRequiresFirstRouteBinding(t *testing.T) {
	v := model.ModelConfig{Alias: "  campus-smart  ", Status: 1}
	if err := validateModel(&v, true); err == "" {
		t.Fatal("creating a model without a first-route binding must be rejected")
	}
	if v.Alias != "campus-smart" {
		t.Fatalf("alias was not trimmed: %q", v.Alias)
	}

	ok := model.ModelConfig{
		Alias: "campus-smart", ProviderID: "6ba7b811-9dad-11d1-80b4-00c04fd430c8", UpstreamModel: "qwen-max",
	}
	if err := validateModel(&ok, true); err != "" {
		t.Fatalf("a complete binding must pass: %s", err)
	}
	if ok.Status != 1 {
		t.Fatalf("status default = %d, want 1", ok.Status)
	}
}

// An update may carry only the model's identity and lifecycle state. That is
// the path the console uses, and it must not touch any Route.
func TestValidateModelUpdateAcceptsIdentityOnly(t *testing.T) {
	v := model.ModelConfig{Alias: "campus-smart"}
	if err := validateModel(&v, false); err != "" {
		t.Fatalf("an identity-only update must pass: %s", err)
	}
	if v.Status != 1 {
		t.Fatalf("status default = %d, want 1", v.Status)
	}
}

// Half a binding would name a channel with no upstream model, or the reverse.
// Whatever wrote that Route would leave it unusable, so neither half alone is
// accepted.
func TestValidateModelRejectsHalfABinding(t *testing.T) {
	for name, v := range map[string]model.ModelConfig{
		"provider only":       {Alias: "m", ProviderID: "6ba7b811-9dad-11d1-80b4-00c04fd430c8"},
		"upstream model only": {Alias: "m", UpstreamModel: "qwen-max"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateModel(&v, false); err == "" {
				t.Fatalf("half a binding must be rejected: %+v", v)
			}
		})
	}
}

func TestValidateModelRejectsMissingAliasAndBadStatus(t *testing.T) {
	noAlias := model.ModelConfig{ProviderID: "p", UpstreamModel: "u"}
	if err := validateModel(&noAlias, true); err == "" {
		t.Fatal("a model without an alias must be rejected")
	}

	badStatus := model.ModelConfig{Alias: "m", Status: 3}
	if err := validateModel(&badStatus, false); err == "" {
		t.Fatal("a status other than enabled/disabled must be rejected")
	}
}
