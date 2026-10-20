package app

import (
	"net/url"
	"testing"
)

// The model list lives under the channel's configured base URL, which already
// ends in /v1, /api/v3 or /api/paas/v4. The refresh path is therefore /models,
// not /v1/models: inserting /v1 would ask Ark for /api/v3/v1/models and GLM for
// /api/paas/v4/v1/models, both of which are 404s.
func TestProviderModelsProbePathAppendsToTheConfiguredBase(t *testing.T) {
	cases := []struct{ name, base, want string }{
		{"openai-style v1", "https://api.deepseek.com/v1", "https://api.deepseek.com/v1/models"},
		{"ark v3", "https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/v3/models"},
		{"glm v4", "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4/models"},
		{"trailing slash", "https://api.deepseek.com/v1/", "https://api.deepseek.com/v1/models"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := directTargetURL(tc.base, &url.URL{Path: providerModelsPath})
			if err != nil {
				t.Fatalf("directTargetURL: %v", err)
			}
			if got != tc.want {
				t.Fatalf("probe URL = %q, want %q", got, tc.want)
			}
		})
	}
}

// The upstream list is an external wire format, so its parser is pinned
// directly: the refresh merges data[].id, ignores blanks and repeats, and
// refuses a body that is not a model list rather than treating it as "none".
func TestParseUpstreamModelIDs(t *testing.T) {
	got, err := parseUpstreamModelIDs([]byte(`{"object":"list","data":[
		{"id":"qwen-max"},{"id":" qwen-plus "},{"id":"qwen-max"},{"id":""}
	]}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got[0] != "qwen-max" || got[1] != "qwen-plus" {
		t.Fatalf("parsed %v, want [qwen-max qwen-plus]", got)
	}

	if _, err := parseUpstreamModelIDs([]byte(`{"object":"list"}`)); err == nil {
		t.Fatal("a body with no data array is not a model list")
	}
	if _, err := parseUpstreamModelIDs([]byte(`not json`)); err == nil {
		t.Fatal("a non-JSON body is not a model list")
	}
}

func TestProviderModelPaths(t *testing.T) {
	id := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	other := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

	if got, ok := providerIDFromModelsPath("/admin/providers/" + id + "/models"); !ok || got != id {
		t.Fatalf("models path = %q, %v", got, ok)
	}
	if got, ok := providerIDFromModelsRefreshPath("/admin/providers/" + id + "/models/refresh"); !ok || got != id {
		t.Fatalf("refresh path = %q, %v", got, ok)
	}
	if gotProvider, gotModel, ok := providerModelIDsFromPath("/admin/providers/" + id + "/models/" + other); !ok || gotProvider != id || gotModel != other {
		t.Fatalf("model path = %q, %q, %v", gotProvider, gotModel, ok)
	}

	for _, path := range []string{
		"/admin/providers/" + id,
		"/admin/providers//models",
		"/admin/providers/not-a-uuid/models",
		"/admin/providers/" + id + "/models/refresh/extra",
	} {
		if _, ok := providerIDFromModelsPath(path); ok {
			t.Errorf("%q unexpectedly parsed as a models path", path)
		}
	}
	if _, ok := providerIDFromModelsRefreshPath("/admin/providers/not-a-uuid/models/refresh"); ok {
		t.Error("a non-UUID provider id must not parse")
	}
	if _, _, ok := providerModelIDsFromPath("/admin/providers/" + id + "/models/refresh"); ok {
		t.Error("refresh is not a model id")
	}
}
