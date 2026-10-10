package app

import (
	"testing"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

func TestModelIDFromRoutesPath(t *testing.T) {
	id := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	got, ok := modelIDFromRoutesPath("/admin/models/" + id + "/routes")
	if !ok || got != id {
		t.Fatalf("got %q, %v; want %q, true", got, ok, id)
	}

	for _, path := range []string{
		"/admin/models/routes",
		"/admin/models//routes",
		"/admin/models/a/b/routes",
		"/admin/models/" + id,
	} {
		if got, ok := modelIDFromRoutesPath(path); ok {
			t.Fatalf("path %q unexpectedly parsed as %q", path, got)
		}
	}
}

func TestValidateModelRouteDefaults(t *testing.T) {
	v := model.ModelRoute{
		ProviderID:    "6ba7b811-9dad-11d1-80b4-00c04fd430c8",
		UpstreamModel: "  upstream-model  ",
	}
	if err := validateModelRoute(&v); err != "" {
		t.Fatalf("unexpected validation error: %s", err)
	}
	if v.UpstreamModel != "upstream-model" {
		t.Fatalf("upstream_model=%q", v.UpstreamModel)
	}
	if v.Priority != 1 || v.Weight != 100 || v.TimeoutMS != 60000 || v.Status != 1 {
		t.Fatalf("unexpected defaults: priority=%d weight=%d timeout=%d status=%d",
			v.Priority, v.Weight, v.TimeoutMS, v.Status)
	}
}

func TestValidateModelRouteRejectsInvalidValues(t *testing.T) {
	base := model.ModelRoute{
		ProviderID:    "6ba7b811-9dad-11d1-80b4-00c04fd430c8",
		UpstreamModel: "model",
		Priority:      1,
		Weight:        100,
		TimeoutMS:     60000,
		Status:        1,
	}
	cases := []struct {
		name string
		edit func(*model.ModelRoute)
	}{
		{"provider", func(v *model.ModelRoute) { v.ProviderID = "not-a-uuid" }},
		{"upstream", func(v *model.ModelRoute) { v.UpstreamModel = "" }},
		{"priority", func(v *model.ModelRoute) { v.Priority = -1 }},
		{"weight", func(v *model.ModelRoute) { v.Weight = -1 }},
		{"timeout", func(v *model.ModelRoute) { v.TimeoutMS = -1 }},
		{"status", func(v *model.ModelRoute) { v.Status = 3 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			tc.edit(&v)
			if err := validateModelRoute(&v); err == "" {
				t.Fatal("expected validation error")
			}
		})
	}
}
